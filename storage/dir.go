package storage

import (
	"cmp"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Dir returns a bucket in a directory on disk, created when first
// written. It suits one server, and dev: `loom dev` keeps an app's files
// in .loom/storage when Config.Storage is nil.
//
// Each object is one file, so a Put is atomic (written aside, then
// renamed). On disk a key's segments are directories named "d.<seg>"
// and the object a file named "f.<seg>", so "a" and "a/b" can both be
// keys, as on S3. The object's details follow its contents in the same
// file. Don't edit the directory by hand; use the bucket.
func Dir(path string) Bucket { return &dir{root: path} }

type dir struct{ root string }

// dirMeta is what a file keeps after an object's contents.
type dirMeta struct {
	ContentType  string    `json:"content_type"`
	CacheControl string    `json:"cache_control,omitempty"`
	ETag         string    `json:"etag"`
	Modified     time.Time `json:"modified"`
}

// path is the file of key.
func (d *dir) path(key string) string {
	segs := strings.Split(key, "/")
	parts := []string{d.root}
	for _, s := range segs[:len(segs)-1] {
		parts = append(parts, "d."+s)
	}
	return filepath.Join(append(parts, "f."+segs[len(segs)-1])...)
}

func (d *dir) Put(ctx context.Context, key string, body io.Reader, size int64, o PutOptions) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if size < 0 {
		return sizeError(key, size, 0)
	}
	tmpDir := filepath.Join(d.root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return d.ioError("write", key, err)
	}
	f, err := os.CreateTemp(tmpDir, "put-*")
	if err != nil {
		return d.ioError("write", key, err)
	}
	defer os.Remove(f.Name()) // after the rename, a no-op
	defer f.Close()
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, size+1))
	if err != nil {
		return err
	}
	if n != size {
		return sizeError(key, size, n)
	}
	meta, _ := json.Marshal(dirMeta{
		ContentType:  cmp.Or(o.ContentType, DefaultContentType),
		CacheControl: o.CacheControl,
		ETag:         `"` + hex.EncodeToString(h.Sum(nil)) + `"`,
		Modified:     time.Now().UTC().Truncate(time.Second),
	})
	if _, err := f.Write(binary.BigEndian.AppendUint32(meta, uint32(len(meta)))); err != nil {
		return d.ioError("write", key, err)
	}
	if err := f.Close(); err != nil {
		return d.ioError("write", key, err)
	}
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return d.ioError("write", key, err)
	}
	if err := os.Rename(f.Name(), p); err != nil {
		return d.ioError("write", key, err)
	}
	return nil
}

// open opens key's file and reads its details; the file is positioned
// at the start of the contents.
func (d *dir) open(key string) (*os.File, Object, error) {
	if err := ValidKey(key); err != nil {
		return nil, Object{}, err
	}
	f, err := os.Open(d.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Object{}, notFound(key)
	}
	if err != nil {
		return nil, Object{}, d.ioError("read", key, err)
	}
	obj, err := readMeta(f, key)
	if err != nil {
		f.Close()
		return nil, Object{}, d.ioError("read", key, err)
	}
	return f, obj, nil
}

// readMeta reads the details at the end of an object's file.
func readMeta(f *os.File, key string) (Object, error) {
	st, err := f.Stat()
	if err != nil {
		return Object{}, err
	}
	var n [4]byte
	if st.Size() < 4 {
		return Object{}, errors.New("not an object file")
	}
	if _, err := f.ReadAt(n[:], st.Size()-4); err != nil {
		return Object{}, err
	}
	mlen := int64(binary.BigEndian.Uint32(n[:]))
	size := st.Size() - 4 - mlen
	if size < 0 {
		return Object{}, errors.New("not an object file")
	}
	buf := make([]byte, mlen)
	if _, err := f.ReadAt(buf, size); err != nil {
		return Object{}, err
	}
	var m dirMeta
	if err := json.Unmarshal(buf, &m); err != nil {
		return Object{}, fmt.Errorf("not an object file: %w", err)
	}
	return Object{Key: key, Size: size, ContentType: m.ContentType, CacheControl: m.CacheControl, ETag: m.ETag, Modified: m.Modified}, nil
}

func (d *dir) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	f, obj, err := d.open(key)
	if err != nil {
		return nil, Object{}, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.NewSectionReader(f, 0, obj.Size), f}, obj, nil
}

func (d *dir) Stat(ctx context.Context, key string) (Object, error) {
	f, obj, err := d.open(key)
	if err != nil {
		return Object{}, err
	}
	f.Close()
	return obj, nil
}

func (d *dir) Delete(ctx context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	p := d.path(key)
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return d.ioError("delete", key, err)
	}
	// Remove the directories left empty, up to the root.
	for p = filepath.Dir(p); p != filepath.Clean(d.root) && strings.HasPrefix(p, filepath.Clean(d.root)); p = filepath.Dir(p) {
		if os.Remove(p) != nil {
			break // not empty, or gone
		}
	}
	return nil
}

func (d *dir) List(ctx context.Context, prefix, after string, limit int) ([]Object, error) {
	if err := validPrefix(prefix); err != nil {
		return nil, err
	}
	// Walk the deepest directory every matching key is under.
	start, base := d.root, ""
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		for _, s := range strings.Split(prefix[:i], "/") {
			start = filepath.Join(start, "d."+s)
		}
		base = prefix[:i+1]
	}
	var keys []string
	var walk func(dirPath, keyPrefix string) error
	walk = func(dirPath, keyPrefix string) error {
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			switch {
			case e.IsDir() && strings.HasPrefix(name, "d."):
				sub := keyPrefix + name[2:] + "/"
				// Descend only where a key could match the prefix.
				if strings.HasPrefix(sub, prefix) || strings.HasPrefix(prefix, sub) {
					if err := walk(filepath.Join(dirPath, name), sub); err != nil {
						return err
					}
				}
			case !e.IsDir() && strings.HasPrefix(name, "f."):
				if k := keyPrefix + name[2:]; strings.HasPrefix(k, prefix) && k > after {
					keys = append(keys, k)
				}
			}
		}
		return nil
	}
	if err := walk(start, base); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, d.ioError("list", prefix, err)
	}
	slices.Sort(keys)
	keys = keys[:min(len(keys), listLimit(limit))]
	out := make([]Object, 0, len(keys))
	for _, k := range keys {
		obj, err := d.Stat(ctx, k)
		if errors.Is(err, ErrNotFound) {
			continue // deleted since the walk
		}
		if err != nil {
			return nil, err
		}
		out = append(out, listed(obj))
	}
	return out, nil
}

// Check makes the directory and checks it can be written.
func (d *dir) Check(ctx context.Context) error {
	if d.root == "" {
		return &Error{Code: SG002, Msg: "storage.Dir was given no path", Fix: `give it a directory, such as storage.Dir("files")`}
	}
	tmpDir := filepath.Join(d.root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return &Error{Code: SG004, Msg: fmt.Sprintf("can't create the storage directory %s: %v", d.root, err), Fix: "give a directory the app's user can write", Err: err}
	}
	f, err := os.CreateTemp(tmpDir, "check-*")
	if err != nil {
		return &Error{Code: SG004, Msg: fmt.Sprintf("can't write to the storage directory %s: %v", d.root, err), Fix: "give a directory the app's user can write", Err: err}
	}
	f.Close()
	os.Remove(f.Name())
	return nil
}

func (d *dir) Describe() string { return "dir " + d.root }

// ioError is a disk error, as SG003.
func (d *dir) ioError(op, key string, err error) error {
	return &Error{Code: SG003, Msg: fmt.Sprintf("can't %s %q in %s: %v", op, key, d.root, err), Fix: "check the storage directory exists and the app's user can read and write it", Err: err}
}
