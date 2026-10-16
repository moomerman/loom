// Package storage keeps an app's files in a bucket: uploads, generated
// files, anything too big for a database row. One interface, Bucket,
// with three implementations that behave the same:
//
//	storage.S3(storage.S3Config{...}) // any S3-compatible service: AWS S3, Cloudflare R2, MinIO, Backblaze B2, Tigris
//	storage.Dir("files")              // a directory on disk: one server, or dev
//	storage.Memory()                  // in memory: tests
//
// An app gives its bucket in loom.Config.Storage and reads it back with
// app.Storage():
//
//	app := loom.New(loom.Config{Storage: storage.S3(storage.S3Config{
//		Endpoint:        os.Getenv("S3_ENDPOINT"),
//		Region:          os.Getenv("S3_REGION"),
//		Bucket:          os.Getenv("S3_BUCKET"),
//		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
//		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
//	})})
//
//	err := app.Storage().Put(ctx, "avatars/42.png", r, size, storage.PutOptions{ContentType: "image/png"})
//	body, obj, err := app.Storage().Get(ctx, "avatars/42.png") // storage.ErrNotFound if none
//
// A bucket is not part of a database transaction, so write the file
// first and the row that names it second: a rolled-back transaction
// leaves an unused file, never a row naming a missing one. Content-
// addressed keys (the file's hash) make a retried write harmless.
//
// The conformance suite every Bucket must pass is storage/storagetest.
// docs/storage.md is the guide.
package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Bucket stores files ("objects") by key. Keys are paths such as
// "avatars/42.png" (ValidKey). Every implementation behaves the same,
// as storagetest checks.
type Bucket interface {
	// Put stores size bytes from body at key, replacing any object
	// there. Readers see the old object or the new one, never a part.
	Put(ctx context.Context, key string, body io.Reader, size int64, o PutOptions) error
	// Get returns the object at key: its contents, which the caller
	// must close, and its details. ErrNotFound if there is none.
	Get(ctx context.Context, key string) (io.ReadCloser, Object, error)
	// Stat returns the details of the object at key, or ErrNotFound.
	Stat(ctx context.Context, key string) (Object, error)
	// Delete removes the object at key. Deleting a key with no object
	// is not an error.
	Delete(ctx context.Context, key string) error
	// List returns up to limit objects whose keys start with prefix and
	// sort after after, in key order (byte-wise). Pass the last key of
	// one page as after to read the next. Listed objects have Key,
	// Size, ETag and Modified; Stat gives the rest.
	List(ctx context.Context, prefix, after string, limit int) ([]Object, error)
	// Check reports whether the bucket can be used: its configuration
	// is complete (SG002), the service answers (SG003), accepts the
	// credentials (SG004) and has the bucket (SG005). app.Run calls it
	// before serving.
	Check(ctx context.Context) error
	// Describe says where the bucket is, for logs and `loom storage`,
	// with no secrets: "s3 https://host/bucket", "dir files", "memory".
	Describe() string
}

// PutOptions are an object's HTTP details, returned with it.
type PutOptions struct {
	// ContentType is its media type, such as "image/png". Defaults to
	// "application/octet-stream".
	ContentType string
	// CacheControl is the Cache-Control header to serve it with, if any.
	CacheControl string
}

// An Object is a stored file's details.
type Object struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	ContentType  string    `json:"content_type,omitempty"`  // not set by List
	CacheControl string    `json:"cache_control,omitempty"` // not set by List
	ETag         string    `json:"etag"`                    // the contents' MD5 in hex, quoted: "\"9e10...\""
	Modified     time.Time `json:"modified"`                // when it was put, to the second
}

// MaxKey is the longest key, in bytes.
const MaxKey = 1024

// MaxList is the most objects one List returns, whatever limit asks.
const MaxList = 1000

// DefaultContentType is an object's type when Put gives none.
const DefaultContentType = "application/octet-stream"

// Storage error codes, documented in docs/errors.md.
const (
	SG001 = "SG001" // the app has no storage: loom.Config.Storage is nil outside dev
	SG002 = "SG002" // a bucket's configuration is incomplete or invalid
	SG003 = "SG003" // the storage service can't be reached, or answered with an error
	SG004 = "SG004" // the storage service refused the credentials
	SG005 = "SG005" // the bucket doesn't exist
	SG006 = "SG006" // a key isn't a valid object key
	SG007 = "SG007" // no object at the key (ErrNotFound)
	SG008 = "SG008" // Put's body was shorter or longer than its size
)

// Error is a storage error with a code and a fix.
type Error struct {
	Code string `json:"code"` // e.g. "SG004"
	Msg  string `json:"message"`
	Fix  string `json:"fix"`
	Err  error  `json:"-"`
}

func (e *Error) Error() string {
	s := fmt.Sprintf("error %s: %s", e.Code, e.Msg)
	if e.Fix != "" {
		s += "\n  fix: " + e.Fix
	}
	return s
}

// Unwrap returns the underlying error, such as a network error.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is an *Error with the same code, so
// errors.Is(err, storage.ErrNotFound) holds for any SG007.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// ErrNotFound is returned by Get and Stat when there is no object at
// the key. Test for it with errors.Is.
var ErrNotFound error = &Error{Code: SG007, Msg: "no object at that key"}

// notFound is ErrNotFound naming the key.
func notFound(key string) error {
	return &Error{Code: SG007, Msg: fmt.Sprintf("no object at %q", key), Fix: "check the key, or treat errors.Is(err, storage.ErrNotFound) as missing"}
}

// ValidKey returns an SG006 error if key can't name an object. A key is
// 1 to 1024 bytes of UTF-8, of segments separated by "/", none empty,
// "." or "..", with no control characters or backslashes. The rules are
// the same for every bucket, so a key that works in tests works on S3
// and on disk.
func ValidKey(key string) error {
	bad := func(why string) error {
		return &Error{Code: SG006, Msg: fmt.Sprintf("%q isn't a valid object key: %s", key, why),
			Fix: `use a path of plain segments, such as "avatars/42.png"`}
	}
	switch {
	case key == "":
		return bad("it is empty")
	case len(key) > MaxKey:
		return bad(fmt.Sprintf("it is %d bytes, over %d", len(key), MaxKey))
	case !utf8.ValidString(key):
		return bad("it isn't UTF-8")
	case strings.ContainsAny(key, "\\"):
		return bad("it has a backslash")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return bad("it has a control character")
		}
	}
	for _, seg := range strings.Split(key, "/") {
		switch seg {
		case "":
			return bad(`it has an empty segment (a leading, trailing or doubled "/")`)
		case ".", "..":
			return bad(`it has a "." or ".." segment`)
		}
	}
	return nil
}

// validPrefix checks a List prefix: "" or a key's start.
func validPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	// A prefix may end with "/" (a "directory") or part-way through a
	// segment; it is valid if some key starts with it.
	return ValidKey(strings.TrimSuffix(prefix, "/"))
}

// listLimit is limit capped to 1..MaxList.
func listLimit(limit int) int {
	if limit <= 0 || limit > MaxList {
		return MaxList
	}
	return limit
}

// sizeError is SG008.
func sizeError(key string, size, got int64) error {
	return &Error{Code: SG008, Msg: fmt.Sprintf("Put %q said %d bytes but the body had %d", key, size, got),
		Fix: "pass the body's exact length as size, e.g. int64(len(data)) with bytes.NewReader(data)"}
}

// readExactly reads all of body, which must be size bytes (SG008).
func readExactly(key string, body io.Reader, size int64) ([]byte, error) {
	if size < 0 {
		return nil, sizeError(key, size, 0)
	}
	data, err := io.ReadAll(io.LimitReader(body, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size {
		return nil, sizeError(key, size, int64(len(data)))
	}
	return data, nil
}

// Missing is the bucket app.Storage() returns outside dev when
// loom.Config.Storage is nil: every call fails with SG001.
func Missing() Bucket { return missing{} }

type missing struct{}

var errMissing = &Error{Code: SG001, Msg: "the app has no storage (loom.Config.Storage is nil)",
	Fix: "set loom.Config{Storage: storage.S3(...)} for an S3-compatible bucket, or storage.Dir(path) for a directory"}

func (missing) Put(context.Context, string, io.Reader, int64, PutOptions) error { return errMissing }
func (missing) Get(context.Context, string) (io.ReadCloser, Object, error) {
	return nil, Object{}, errMissing
}
func (missing) Stat(context.Context, string) (Object, error) { return Object{}, errMissing }
func (missing) Delete(context.Context, string) error         { return errMissing }
func (missing) List(context.Context, string, string, int) ([]Object, error) {
	return nil, errMissing
}
func (missing) Check(context.Context) error { return nil } // an app that never stores files is fine
func (missing) Describe() string            { return "none" }

// IsMissing reports whether b is the Missing bucket.
func IsMissing(b Bucket) bool {
	_, ok := b.(missing)
	return ok
}
