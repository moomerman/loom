package storage_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/moomerman/loom/storage"
	"github.com/moomerman/loom/storage/storagetest"
)

func TestMemory(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Bucket { return storage.Memory() })
}

func TestDir(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Bucket { return storage.Dir(t.TempDir()) })
}

// s3FromEnv is the S3-compatible service the test environment names:
//
//	LOOM_TEST_S3=http://minioadmin:minioadmin@localhost:9000/loom-test
//
// an endpoint, credentials and a bucket the tests may create, fill and
// empty. Each test empties it first. Without it the S3 tests skip.
func s3FromEnv(t *testing.T) storage.S3Config {
	raw := os.Getenv("LOOM_TEST_S3")
	if raw == "" {
		t.Skip("set LOOM_TEST_S3=http://KEY:SECRET@host:port/bucket to test S3 (e.g. MinIO)")
	}
	scheme, rest, _ := strings.Cut(raw, "://")
	creds, rest, _ := strings.Cut(rest, "@")
	host, bucket, _ := strings.Cut(rest, "/")
	key, secret, _ := strings.Cut(creds, ":")
	cfg := storage.S3Config{Endpoint: scheme + "://" + host, Bucket: bucket, AccessKeyID: key, SecretAccessKey: secret}
	if err := storage.CreateBucket(cfg); err != nil {
		t.Fatalf("creating the test bucket: %v", err)
	}
	return cfg
}

// emptyBucket deletes every object in b.
func emptyBucket(t *testing.T, b storage.Bucket) {
	ctx := context.Background()
	for {
		objs, err := b.List(ctx, "", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(objs) == 0 {
			return
		}
		for _, o := range objs {
			if err := b.Delete(ctx, o.Key); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestS3(t *testing.T) {
	cfg := s3FromEnv(t)
	storagetest.Run(t, func(t *testing.T) storage.Bucket {
		b := storage.S3(cfg)
		emptyBucket(t, b)
		t.Cleanup(func() { emptyBucket(t, b) })
		return b
	})
}

func TestS3Errors(t *testing.T) {
	cfg := s3FromEnv(t)
	ctx := context.Background()
	code := func(err error) string {
		var e *storage.Error
		if errors.As(err, &e) {
			if e.Fix == "" {
				t.Errorf("%s has no fix", e.Code)
			}
			return e.Code
		}
		return "not a *storage.Error: " + errString(err)
	}
	wrongSecret := cfg
	wrongSecret.SecretAccessKey += "x"
	if got := code(storage.S3(wrongSecret).Check(ctx)); got != storage.SG004 {
		t.Errorf("a wrong secret: %s, want SG004", got)
	}
	if got := code(storage.S3(wrongSecret).Put(ctx, "k", strings.NewReader("x"), 1, storage.PutOptions{})); got != storage.SG004 {
		t.Errorf("Put with a wrong secret: %s, want SG004", got)
	}
	noBucket := cfg
	noBucket.Bucket = "loom-no-such-bucket-" + randomHex()
	if got := code(storage.S3(noBucket).Check(ctx)); got != storage.SG005 {
		t.Errorf("a missing bucket: %s, want SG005", got)
	}
	if got := code(storage.S3(noBucket).Delete(ctx, "k")); got != storage.SG005 {
		t.Errorf("Delete in a missing bucket: %s, want SG005", got)
	}
	unreachable := cfg
	unreachable.Endpoint = "http://127.0.0.1:1"
	if got := code(storage.S3(unreachable).Check(ctx)); got != storage.SG003 {
		t.Errorf("an unreachable endpoint: %s, want SG003", got)
	}
}

func TestS3Config(t *testing.T) {
	ctx := context.Background()
	full := storage.S3Config{Endpoint: "https://example.com", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"}
	for name, change := range map[string]func(*storage.S3Config){
		"no bucket":         func(c *storage.S3Config) { c.Bucket = "" },
		"no key":            func(c *storage.S3Config) { c.AccessKeyID = "" },
		"no secret":         func(c *storage.S3Config) { c.SecretAccessKey = "" },
		"endpoint path":     func(c *storage.S3Config) { c.Endpoint = "https://example.com/bucket" },
		"endpoint scheme":   func(c *storage.S3Config) { c.Endpoint = "example.com" },
		"bucket with slash": func(c *storage.S3Config) { c.Bucket = "b/prefix" },
	} {
		cfg := full
		change(&cfg)
		var e *storage.Error
		if err := storage.S3(cfg).Check(ctx); !errors.As(err, &e) || e.Code != storage.SG002 || e.Fix == "" {
			t.Errorf("%s: Check = %v, want SG002 with a fix", name, err)
		}
		if _, err := storage.S3(cfg).Stat(ctx, "k"); !errors.As(err, &e) || e.Code != storage.SG002 {
			t.Errorf("%s: Stat = %v, want SG002", name, err)
		}
	}
}

func TestDirCheck(t *testing.T) {
	var e *storage.Error
	if err := storage.Dir("").Check(context.Background()); !errors.As(err, &e) || e.Code != storage.SG002 {
		t.Errorf("Dir(\"\").Check = %v, want SG002", err)
	}
	file := t.TempDir() + "/file"
	os.WriteFile(file, nil, 0o644)
	if err := storage.Dir(file + "/sub").Check(context.Background()); !errors.As(err, &e) || e.Code != storage.SG004 {
		t.Errorf("a directory under a file: Check = %v, want SG004", err)
	}
}

func TestMissing(t *testing.T) {
	b := storage.Missing()
	var e *storage.Error
	if err := b.Put(context.Background(), "k", strings.NewReader("x"), 1, storage.PutOptions{}); !errors.As(err, &e) || e.Code != storage.SG001 {
		t.Errorf("Put = %v, want SG001", err)
	}
	if err := b.Check(context.Background()); err != nil {
		t.Errorf("Check = %v; an app that stores nothing needs no storage", err)
	}
	if !storage.IsMissing(b) || storage.IsMissing(storage.Memory()) {
		t.Error("IsMissing is wrong")
	}
}

func TestServeHead(t *testing.T) {
	b := storage.Memory()
	b.Put(context.Background(), "k", strings.NewReader("hello"), 5, storage.PutOptions{})
	r, _ := http.NewRequest("HEAD", "/k", nil)
	w := &recorder{header: http.Header{}}
	storage.Serve(w, r, b, "k")
	if w.code != 200 || w.body.Len() != 0 || w.header.Get("Content-Length") != "5" {
		t.Errorf("HEAD = %d %q %v", w.code, w.body.String(), w.header)
	}
}

type recorder struct {
	header http.Header
	code   int
	body   strings.Builder
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(code int)        { r.code = code }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }

func randomHex() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
