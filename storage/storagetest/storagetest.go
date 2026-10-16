// Package storagetest is the conformance suite every storage.Bucket
// must pass, so that buckets in memory, on disk and on S3 behave the
// same.
//
//	func TestConformance(t *testing.T) {
//		storagetest.Run(t, func(t *testing.T) storage.Bucket { return storage.Dir(t.TempDir()) })
//	}
//
// The factory returns an empty bucket for each test.
package storagetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moomerman/loom/storage"
)

// Factory returns an empty bucket.
type Factory func(t *testing.T) storage.Bucket

// Run runs the conformance suite.
func Run(t *testing.T, f Factory) {
	tests := []struct {
		name string
		fn   func(t *testing.T, b storage.Bucket)
	}{
		{"PutGet", testPutGet},
		{"Replace", testReplace},
		{"Empty", testEmpty},
		{"Missing", testMissing},
		{"Delete", testDelete},
		{"Keys", testKeys},
		{"InvalidKeys", testInvalidKeys},
		{"Size", testSize},
		{"List", testList},
		{"ListPages", testListPages},
		{"Concurrent", testConcurrent},
		{"Serve", testServe},
		{"Check", testCheck},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.fn(t, f(t)) })
	}
}

func put(t *testing.T, b storage.Bucket, key, data string, o storage.PutOptions) {
	t.Helper()
	if err := b.Put(context.Background(), key, strings.NewReader(data), int64(len(data)), o); err != nil {
		t.Fatalf("Put %q: %v", key, err)
	}
}

func get(t *testing.T, b storage.Bucket, key string) (string, storage.Object) {
	t.Helper()
	body, obj, err := b.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get %q: %v", key, err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("Get %q: reading: %v", key, err)
	}
	return string(data), obj
}

// md5 of "hello, world"
const helloETag = `"e4d7f1b4ed2e42d15898f4b27b019da4"`

func testPutGet(t *testing.T, b storage.Bucket) {
	before := time.Now().UTC().Add(-2 * time.Second)
	put(t, b, "notes/hello.txt", "hello, world", storage.PutOptions{ContentType: "text/plain; charset=utf-8", CacheControl: "public, max-age=60"})
	data, obj := get(t, b, "notes/hello.txt")
	if data != "hello, world" {
		t.Errorf("Get = %q, want hello, world", data)
	}
	want := storage.Object{Key: "notes/hello.txt", Size: 12, ContentType: "text/plain; charset=utf-8", CacheControl: "public, max-age=60", ETag: helloETag}
	if obj.Modified.Before(before) || obj.Modified.After(time.Now().Add(2*time.Second)) || obj.Modified.Location() != time.UTC {
		t.Errorf("Modified = %v, want about now, in UTC", obj.Modified)
	}
	obj.Modified = time.Time{}
	if obj != want {
		t.Errorf("Get's object = %+v, want %+v", obj, want)
	}
	st, err := b.Stat(context.Background(), "notes/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	st.Modified = time.Time{}
	if st != want {
		t.Errorf("Stat = %+v, want %+v", st, want)
	}
	// No content type is application/octet-stream.
	put(t, b, "raw", "x", storage.PutOptions{})
	if _, obj := get(t, b, "raw"); obj.ContentType != storage.DefaultContentType || obj.CacheControl != "" {
		t.Errorf("with no options, ContentType = %q, CacheControl = %q", obj.ContentType, obj.CacheControl)
	}
}

func testReplace(t *testing.T, b storage.Bucket) {
	put(t, b, "a", "first", storage.PutOptions{ContentType: "text/plain", CacheControl: "no-store"})
	put(t, b, "a", "second, longer", storage.PutOptions{ContentType: "text/csv"})
	data, obj := get(t, b, "a")
	if data != "second, longer" || obj.Size != 14 || obj.ContentType != "text/csv" || obj.CacheControl != "" {
		t.Errorf("after replacing, Get = %q %+v", data, obj)
	}
}

func testEmpty(t *testing.T, b storage.Bucket) {
	put(t, b, "empty", "", storage.PutOptions{})
	data, obj := get(t, b, "empty")
	if data != "" || obj.Size != 0 || obj.ETag != `"d41d8cd98f00b204e9800998ecf8427e"` {
		t.Errorf("empty object: Get = %q %+v", data, obj)
	}
}

func testMissing(t *testing.T, b storage.Bucket) {
	ctx := context.Background()
	if _, _, err := b.Get(ctx, "nothing/here"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get of a missing key = %v, want ErrNotFound", err)
	}
	if _, err := b.Stat(ctx, "nothing/here"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat of a missing key = %v, want ErrNotFound", err)
	}
	// A key that is only a prefix of others isn't an object.
	put(t, b, "dir/file", "x", storage.PutOptions{})
	if _, err := b.Stat(ctx, "dir"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat of a prefix = %v, want ErrNotFound", err)
	}
	var e *storage.Error
	if _, err := b.Stat(ctx, "nothing"); !errors.As(err, &e) || e.Code != storage.SG007 || e.Fix == "" {
		t.Errorf("not found error = %#v, want an SG007 *storage.Error with a fix", err)
	}
}

func testDelete(t *testing.T, b storage.Bucket) {
	ctx := context.Background()
	put(t, b, "a/b/c", "x", storage.PutOptions{})
	put(t, b, "a/d", "y", storage.PutOptions{})
	if err := b.Delete(ctx, "a/b/c"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(ctx, "a/b/c"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after Delete, Stat = %v, want ErrNotFound", err)
	}
	if data, _ := get(t, b, "a/d"); data != "y" {
		t.Errorf("Delete removed a neighbour")
	}
	if err := b.Delete(ctx, "a/b/c"); err != nil {
		t.Errorf("deleting a missing key = %v, want nil", err)
	}
	if err := b.Delete(ctx, "never"); err != nil {
		t.Errorf("deleting a never-put key = %v, want nil", err)
	}
	objs, err := b.List(ctx, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(objs); !slices.Equal(got, []string{"a/d"}) {
		t.Errorf("after Delete, List = %v", got)
	}
}

func testKeys(t *testing.T, b storage.Bucket) {
	// A key may hold any UTF-8 but control characters. (Keys that are
	// also another key's directory, "a" and "a/b", aren't tested: MinIO
	// can't store both.)
	ks := []string{"a", "a b+c=d&e", "ümlaut/日本語.txt", "x%20y", "dots...", ".hidden/.file", "a-b_c~d", "tmp", "d.x", "f.y", "with?query#frag", "it's (1) [2] {3} $4 @5 !6 *7 ;8 ,9 :10"}
	for _, k := range ks {
		put(t, b, k, "data of "+k, storage.PutOptions{})
	}
	for _, k := range ks {
		if data, obj := get(t, b, k); data != "data of "+k || obj.Key != k {
			t.Errorf("Get %q = %q, key %q", k, data, obj.Key)
		}
	}
	objs, err := b.List(context.Background(), "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(ks)
	slices.Sort(want)
	if got := keys(objs); !slices.Equal(got, want) {
		t.Errorf("List = %q\nwant %q", got, want)
	}
}

func testInvalidKeys(t *testing.T, b storage.Bucket) {
	ctx := context.Background()
	for _, k := range []string{"", "/a", "a/", "a//b", "./a", "a/../b", "a\\b", "a\x00b", "a\nb", strings.Repeat("k", 1025), "\xff"} {
		var e *storage.Error
		if err := b.Put(ctx, k, strings.NewReader("x"), 1, storage.PutOptions{}); !errors.As(err, &e) || e.Code != storage.SG006 {
			t.Errorf("Put %q = %v, want SG006", k, err)
		}
		if _, _, err := b.Get(ctx, k); !errors.As(err, &e) || e.Code != storage.SG006 {
			t.Errorf("Get %q = %v, want SG006", k, err)
		}
		if err := b.Delete(ctx, k); !errors.As(err, &e) || e.Code != storage.SG006 {
			t.Errorf("Delete %q = %v, want SG006", k, err)
		}
	}
	if err := storage.ValidKey(strings.Repeat("k", 1024)); err != nil {
		t.Errorf("a 1024-byte key: %v", err)
	}
}

func testSize(t *testing.T, b storage.Bucket) {
	ctx := context.Background()
	var e *storage.Error
	if err := b.Put(ctx, "short", strings.NewReader("abc"), 5, storage.PutOptions{}); !errors.As(err, &e) || e.Code != storage.SG008 {
		t.Errorf("Put with a short body = %v, want SG008", err)
	}
	if err := b.Put(ctx, "long", strings.NewReader("abcdef"), 3, storage.PutOptions{}); !errors.As(err, &e) || e.Code != storage.SG008 {
		t.Errorf("Put with a long body = %v, want SG008", err)
	}
	for _, k := range []string{"short", "long"} {
		if _, err := b.Stat(ctx, k); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("after a Put with the wrong size, Stat %q = %v, want ErrNotFound", k, err)
		}
	}
	// A large object streams through.
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MB
	if err := b.Put(ctx, "big", bytes.NewReader(big), int64(len(big)), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if data, obj := get(t, b, "big"); !bytes.Equal([]byte(data), big) || obj.Size != int64(len(big)) {
		t.Errorf("big object: got %d bytes, size %d", len(data), obj.Size)
	}
}

func testList(t *testing.T, b storage.Bucket) {
	ctx := context.Background()
	for _, k := range []string{"img/b.png", "img/a.png", "img/sub/c.png", "imgx", "doc/a", "img-1"} {
		put(t, b, k, k, storage.PutOptions{ContentType: "image/png"})
	}
	cases := []struct {
		prefix, after string
		limit         int
		want          []string
	}{
		{"", "", 0, []string{"doc/a", "img-1", "img/a.png", "img/b.png", "img/sub/c.png", "imgx"}},
		{"img/", "", 0, []string{"img/a.png", "img/b.png", "img/sub/c.png"}},
		{"img", "", 0, []string{"img-1", "img/a.png", "img/b.png", "img/sub/c.png", "imgx"}},
		{"img/s", "", 0, []string{"img/sub/c.png"}},
		{"img/", "img/a.png", 0, []string{"img/b.png", "img/sub/c.png"}},
		{"img/", "", 2, []string{"img/a.png", "img/b.png"}},
		{"", "img/b", 0, []string{"img/b.png", "img/sub/c.png", "imgx"}},
		{"", "zzz", 0, nil},
		{"nothing/", "", 0, nil},
	}
	for _, c := range cases {
		objs, err := b.List(ctx, c.prefix, c.after, c.limit)
		if err != nil {
			t.Fatalf("List(%q, %q, %d): %v", c.prefix, c.after, c.limit, err)
		}
		if got := keys(objs); !slices.Equal(got, c.want) {
			t.Errorf("List(%q, %q, %d) = %q, want %q", c.prefix, c.after, c.limit, got, c.want)
		}
	}
	objs, _ := b.List(ctx, "doc/", "", 0)
	if len(objs) != 1 {
		t.Fatalf("List doc/ = %v", objs)
	}
	o := objs[0]
	if o.Size != 5 || o.ETag == "" || o.Modified.IsZero() || o.ContentType != "" || o.CacheControl != "" {
		t.Errorf("a listed object = %+v, want Key, Size, ETag and Modified only", o)
	}
	if st, _ := b.Stat(ctx, "doc/a"); st.ETag != o.ETag || !st.Modified.Equal(o.Modified) {
		t.Errorf("List's ETag and Modified %q %v differ from Stat's %q %v", o.ETag, o.Modified, st.ETag, st.Modified)
	}
}

func testListPages(t *testing.T, b storage.Bucket) {
	ctx := context.Background()
	var want []string
	for i := range 25 {
		k := fmt.Sprintf("p/%02d", i)
		put(t, b, k, "x", storage.PutOptions{})
		want = append(want, k)
	}
	var got []string
	after := ""
	for {
		objs, err := b.List(ctx, "p/", after, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(objs) == 0 {
			break
		}
		if len(objs) > 10 {
			t.Fatalf("List with limit 10 gave %d", len(objs))
		}
		got = append(got, keys(objs)...)
		after = objs[len(objs)-1].Key
	}
	if !slices.Equal(got, want) {
		t.Errorf("paged List = %v, want %v", got, want)
	}
}

func testConcurrent(t *testing.T, b storage.Bucket) {
	// Readers see a whole object, old or new, never a mix.
	ctx := context.Background()
	old, new := strings.Repeat("o", 64<<10), strings.Repeat("n", 64<<10)
	put(t, b, "c", old, storage.PutOptions{})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := old
			if i%2 == 0 {
				data = new
			}
			if err := b.Put(ctx, "c", strings.NewReader(data), int64(len(data)), storage.PutOptions{}); err != nil {
				t.Error(err)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _, err := b.Get(ctx, "c")
			if err != nil {
				t.Error(err)
				return
			}
			defer body.Close()
			data, err := io.ReadAll(body)
			if err != nil {
				t.Error(err)
				return
			}
			if s := string(data); s != old && s != new {
				t.Errorf("a reader saw %d bytes mixing two writes", len(s))
			}
		}()
	}
	wg.Wait()
}

func testServe(t *testing.T, b storage.Bucket) {
	put(t, b, "s/page.html", "<p>hi</p>", storage.PutOptions{ContentType: "text/html; charset=utf-8", CacheControl: "public, max-age=3600"})
	serve := func(key string, header http.Header) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/x", nil)
		maps(r.Header, header)
		w := httptest.NewRecorder()
		if err := storage.Serve(w, r, b, key); err != nil {
			t.Errorf("Serve %q: %v", key, err)
		}
		return w
	}
	w := serve("s/page.html", nil)
	if w.Code != 200 || w.Body.String() != "<p>hi</p>" || w.Header().Get("Content-Type") != "text/html; charset=utf-8" ||
		w.Header().Get("Cache-Control") != "public, max-age=3600" || w.Header().Get("Content-Length") != "9" ||
		w.Header().Get("ETag") == "" || w.Header().Get("Last-Modified") == "" {
		t.Errorf("Serve = %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if w := serve("s/page.html", http.Header{"If-None-Match": {w.Header().Get("ETag")}}); w.Code != 304 || w.Body.Len() != 0 {
		t.Errorf("Serve with a matching If-None-Match = %d %q, want 304", w.Code, w.Body.String())
	}
	if w := serve("s/missing", nil); w.Code != 404 {
		t.Errorf("Serve of a missing key = %d, want 404", w.Code)
	}
	if w := serve("../etc/passwd", nil); w.Code != 404 {
		t.Errorf("Serve of an invalid key = %d, want 404", w.Code)
	}
}

func testCheck(t *testing.T, b storage.Bucket) {
	if err := b.Check(context.Background()); err != nil {
		t.Errorf("Check = %v", err)
	}
	if b.Describe() == "" {
		t.Error("Describe is empty")
	}
}

func keys(objs []storage.Object) []string {
	var ks []string
	for _, o := range objs {
		ks = append(ks, o.Key)
	}
	return ks
}

func maps(dst, src http.Header) {
	for k, v := range src {
		dst[k] = v
	}
}
