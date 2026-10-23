package storage

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// FakeS3 starts an S3-compatible service in memory, for the S3 tests
// when no real one is given, and returns the config of a bucket on it.
// It serves what the s3 bucket sends: create and head bucket, put, get,
// head and delete object, and ListObjectsV2, with the status codes and
// XML errors S3 answers, and it checks each request's Signature Version
// 4 with the same signer, so a wrong key is refused as S3 refuses it.
func FakeS3(t *testing.T) S3Config {
	t.Helper()
	f := &fakeS3{keyID: "loom", secret: "loom-secret", buckets: map[string]map[string]*fakeObject{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return S3Config{Endpoint: srv.URL, Bucket: "loom-test", AccessKeyID: f.keyID, SecretAccessKey: f.secret}
}

type fakeS3 struct {
	keyID, secret string
	mu            sync.Mutex
	buckets       map[string]map[string]*fakeObject // by bucket, then key
}

type fakeObject struct {
	data                      []byte
	contentType, cacheControl string
	etag                      string
	modified                  time.Time
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		f.fail(w, http.StatusForbidden, "SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided.")
		return
	}
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	f.mu.Lock()
	defer f.mu.Unlock()
	objects, ok := f.buckets[bucket]
	if key == "" {
		switch {
		case r.Method == "PUT":
			if !ok {
				f.buckets[bucket] = map[string]*fakeObject{}
			}
			w.WriteHeader(http.StatusOK)
		case !ok:
			f.fail(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		case r.Method == "HEAD":
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Query().Get("list-type") == "2":
			f.list(w, objects, r.URL.Query())
		default:
			f.fail(w, http.StatusNotImplemented, "NotImplemented", r.Method+" on a bucket")
		}
		return
	}
	if !ok {
		f.fail(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
		return
	}
	switch r.Method {
	case "PUT":
		data, err := io.ReadAll(r.Body)
		if err != nil {
			f.fail(w, http.StatusBadRequest, "IncompleteBody", err.Error())
			return
		}
		sum := md5.Sum(data)
		objects[key] = &fakeObject{
			data:         data,
			contentType:  r.Header.Get("Content-Type"),
			cacheControl: r.Header.Get("Cache-Control"),
			etag:         `"` + hex.EncodeToString(sum[:]) + `"`,
			modified:     time.Now().UTC().Truncate(time.Second),
		}
		w.Header().Set("ETag", objects[key].etag)
		w.WriteHeader(http.StatusOK)
	case "GET", "HEAD":
		o, ok := objects[key]
		if !ok {
			f.fail(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		h := w.Header()
		h.Set("Content-Type", o.contentType)
		if o.cacheControl != "" {
			h.Set("Cache-Control", o.cacheControl)
		}
		h.Set("ETag", o.etag)
		h.Set("Last-Modified", o.modified.Format(http.TimeFormat))
		h.Set("Content-Length", strconv.Itoa(len(o.data)))
		w.WriteHeader(http.StatusOK)
		if r.Method == "GET" {
			w.Write(o.data)
		}
	case "DELETE":
		delete(objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		f.fail(w, http.StatusNotImplemented, "NotImplemented", r.Method+" on an object")
	}
}

// list answers ListObjectsV2: the keys with the prefix, after
// start-after, in order, up to max-keys of them.
func (f *fakeS3) list(w http.ResponseWriter, objects map[string]*fakeObject, q url.Values) {
	prefix, after := q.Get("prefix"), q.Get("start-after")
	limit, _ := strconv.Atoi(q.Get("max-keys"))
	keys := make([]string, 0, len(objects))
	for k := range objects {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	type contents struct {
		Key          string
		LastModified string
		ETag         string
		Size         int
	}
	var res struct {
		XMLName  xml.Name `xml:"ListBucketResult"`
		Contents []contents
	}
	for _, k := range keys {
		o := objects[k]
		res.Contents = append(res.Contents, contents{k, o.modified.Format("2006-01-02T15:04:05.000Z"), o.etag, len(o.data)})
	}
	w.Header().Set("Content-Type", "application/xml")
	xml.NewEncoder(w).Encode(res)
}

// fail answers with an S3 error document.
func (f *fakeS3) fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<Error><Code>%s</Code><Message>%s</Message></Error>", code, message)
}

// authorized reports whether the request's Authorization header is the
// one signV4 would give it with the fake's credentials: the signed
// headers are copied to a request of the same method and URL and signed
// again, with the key, region and date the header names.
func (f *fakeS3) authorized(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	fields := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ", ") {
		k, v, _ := strings.Cut(part, "=")
		fields[k] = v
	}
	scope := strings.Split(fields["Credential"], "/") // key, day, region, s3, aws4_request
	if len(scope) != 5 || scope[0] != f.keyID {
		return false
	}
	at, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return false
	}
	u := *r.URL
	u.Host = r.Host
	req := &http.Request{Method: r.Method, URL: &u, Header: http.Header{}}
	for _, name := range strings.Split(fields["SignedHeaders"], ";") {
		if name != "host" {
			req.Header[http.CanonicalHeaderKey(name)] = r.Header.Values(name)
		}
	}
	signV4(req, r.Header.Get("X-Amz-Content-Sha256"), at, scope[0], f.secret, scope[2])
	return hmac.Equal([]byte(req.Header.Get("Authorization")), []byte(auth))
}
