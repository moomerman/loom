package storage

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// S3Config says where an S3-compatible bucket is and how to sign in.
type S3Config struct {
	// Endpoint is the service's address, scheme and host, such as
	// "https://<account>.r2.cloudflarestorage.com" (Cloudflare R2),
	// "https://s3.us-west-004.backblazeb2.com" (Backblaze B2),
	// "https://fly.storage.tigris.dev" (Tigris) or
	// "http://localhost:9000" (MinIO). Empty means AWS S3 in Region.
	Endpoint string
	// Region is the bucket's region, which signs every request.
	// Defaults to "us-east-1"; Cloudflare R2 uses "auto".
	Region string
	// Bucket is the bucket's name. It must exist: Loom never creates it.
	Bucket string
	// AccessKeyID and SecretAccessKey are the credentials, from the
	// service's console, with permission to read, write, delete and
	// list objects in the bucket. Read them from the environment.
	AccessKeyID     string
	SecretAccessKey string
	// Client sends the requests. Defaults to an http.Client with a
	// 30-second timeout per request.
	Client *http.Client
}

// S3 returns the bucket an S3Config describes. Objects are addressed
// path-style (https://endpoint/bucket/key), which every S3-compatible
// service supports, and requests are signed with AWS Signature Version
// 4. An incomplete config isn't an error until the bucket is used, or
// app.Run checks it (SG002).
func S3(cfg S3Config) Bucket {
	cfg.Region = cmp.Or(cfg.Region, "us-east-1")
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://s3." + cfg.Region + ".amazonaws.com"
	}
	cfg.Endpoint = strings.TrimSuffix(cfg.Endpoint, "/")
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &s3{cfg: cfg, now: time.Now}
}

type s3 struct {
	cfg S3Config
	now func() time.Time
}

func (b *s3) Describe() string { return "s3 " + b.cfg.Endpoint + "/" + b.cfg.Bucket }

// configError returns SG002 if the config can't make requests.
func (b *s3) configError() error {
	bad := func(msg, fix string) error { return &Error{Code: SG002, Msg: "storage.S3Config " + msg, Fix: fix} }
	u, err := url.Parse(b.cfg.Endpoint)
	switch {
	case b.cfg.Bucket == "":
		return bad("has no Bucket", "give the bucket's name, e.g. Bucket: os.Getenv(\"S3_BUCKET\"); create the bucket in the service's console")
	case b.cfg.AccessKeyID == "" || b.cfg.SecretAccessKey == "":
		return bad("has no AccessKeyID or SecretAccessKey", "give the credentials from the environment, e.g. AccessKeyID: os.Getenv(\"S3_ACCESS_KEY_ID\")")
	case err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/"):
		return bad(fmt.Sprintf("Endpoint %q isn't a scheme and host", b.cfg.Endpoint), `give the service's address with no path, e.g. "https://<account>.r2.cloudflarestorage.com"; the bucket goes in Bucket`)
	case strings.Contains(b.cfg.Bucket, "/"):
		return bad(fmt.Sprintf("Bucket %q has a /", b.cfg.Bucket), "give the bucket's name alone; keep paths in object keys")
	}
	return nil
}

// objectURL is the URL of key, or of the bucket for "".
func (b *s3) objectURL(key string, query url.Values) string {
	u := b.cfg.Endpoint + "/" + escapePath(b.cfg.Bucket)
	if key != "" {
		u += "/" + escapePath(key)
	}
	if len(query) > 0 {
		u += "?" + canonicalQuery(query)
	}
	return u
}

// do signs and sends a request; body may be nil. payloadHash is the
// body's SHA-256 in hex, or "UNSIGNED-PAYLOAD".
func (b *s3) do(ctx context.Context, method, rawURL string, header http.Header, body io.Reader, size int64, payloadHash string) (*http.Response, error) {
	if err := b.configError(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		if size == 0 {
			req.Body = http.NoBody
		}
	}
	for k, v := range header {
		req.Header[k] = v
	}
	signV4(req, payloadHash, b.now(), b.cfg.AccessKeyID, b.cfg.SecretAccessKey, b.cfg.Region)
	res, err := b.cfg.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Code: SG003, Msg: fmt.Sprintf("can't reach the storage service at %s: %v", b.cfg.Endpoint, err),
			Fix: "check S3Config.Endpoint and that the service is up", Err: err}
	}
	return res, nil
}

// emptyHash is the SHA-256 of no bytes, the payload hash of a request
// with no body.
const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func (b *s3) Put(ctx context.Context, key string, body io.Reader, size int64, o PutOptions) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if size < 0 {
		return sizeError(key, size, 0)
	}
	h := http.Header{"Content-Type": {cmp.Or(o.ContentType, DefaultContentType)}}
	if o.CacheControl != "" {
		h.Set("Cache-Control", o.CacheControl)
	}
	// The payload isn't hashed, so a body can stream without being read
	// twice; TLS protects it in transit.
	counted := &countingReader{r: io.LimitReader(body, size)}
	res, err := b.do(ctx, "PUT", b.objectURL(key, nil), h, counted, size, "UNSIGNED-PAYLOAD")
	if counted.n < size {
		if err == nil {
			res.Body.Close()
		}
		return sizeError(key, size, counted.n)
	}
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return b.responseError(res, "put", key)
	}
	// A body longer than size was cut short: refuse what was stored.
	var one [1]byte
	if n, _ := io.ReadFull(body, one[:]); n > 0 {
		b.Delete(ctx, key)
		return sizeError(key, size, size+1)
	}
	return nil
}

func (b *s3) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	if err := ValidKey(key); err != nil {
		return nil, Object{}, err
	}
	res, err := b.do(ctx, "GET", b.objectURL(key, nil), nil, nil, 0, emptyHash)
	if err != nil {
		return nil, Object{}, err
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		return nil, Object{}, b.responseError(res, "get", key)
	}
	return res.Body, objectOf(key, res), nil
}

func (b *s3) Stat(ctx context.Context, key string) (Object, error) {
	if err := ValidKey(key); err != nil {
		return Object{}, err
	}
	res, err := b.do(ctx, "HEAD", b.objectURL(key, nil), nil, nil, 0, emptyHash)
	if err != nil {
		return Object{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Object{}, b.responseError(res, "stat", key)
	}
	return objectOf(key, res), nil
}

// objectOf reads an object's details from a GET or HEAD response.
func objectOf(key string, res *http.Response) Object {
	modified, _ := http.ParseTime(res.Header.Get("Last-Modified"))
	size := res.ContentLength
	if n, err := strconv.ParseInt(res.Header.Get("Content-Length"), 10, 64); err == nil {
		size = n // HEAD's Content-Length is the object's
	}
	return Object{
		Key:          key,
		Size:         size,
		ContentType:  res.Header.Get("Content-Type"),
		CacheControl: res.Header.Get("Cache-Control"),
		ETag:         res.Header.Get("ETag"),
		Modified:     modified.UTC(),
	}
}

func (b *s3) Delete(ctx context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	res, err := b.do(ctx, "DELETE", b.objectURL(key, nil), nil, nil, 0, emptyHash)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNotFound {
		return b.responseError(res, "delete", key)
	}
	if res.StatusCode == http.StatusNotFound {
		// Deleting nothing is fine, but not in a missing bucket.
		if err := b.responseError(res, "delete", key); errors.Is(err, &Error{Code: SG005}) {
			return err
		}
	}
	return nil
}

// listResult is ListObjectsV2's answer.
type listResult struct {
	Contents []struct {
		Key          string
		LastModified time.Time
		ETag         string
		Size         int64
	}
}

func (b *s3) List(ctx context.Context, prefix, after string, limit int) ([]Object, error) {
	if err := validPrefix(prefix); err != nil {
		return nil, err
	}
	q := url.Values{"list-type": {"2"}, "max-keys": {strconv.Itoa(listLimit(limit))}}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	// start-after names a key to skip; a prefix past it is the same.
	if after != "" {
		q.Set("start-after", after)
	}
	res, err := b.do(ctx, "GET", b.objectURL("", q), nil, nil, 0, emptyHash)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, b.responseError(res, "list", prefix)
	}
	var r listResult
	if err := xml.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, &Error{Code: SG003, Msg: "can't read the storage service's list of objects: " + err.Error(), Fix: "check S3Config.Endpoint is an S3-compatible service", Err: err}
	}
	out := make([]Object, len(r.Contents))
	for i, c := range r.Contents {
		out[i] = Object{Key: c.Key, Size: c.Size, ETag: c.ETag, Modified: c.LastModified.UTC().Truncate(time.Second)}
	}
	return out, nil
}

// Check asks for the bucket (HeadBucket).
func (b *s3) Check(ctx context.Context) error {
	res, err := b.do(ctx, "HEAD", b.objectURL("", nil), nil, nil, 0, emptyHash)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return b.responseError(res, "check", "")
	}
	return nil
}

// s3Error is the XML body of an S3 error response.
type s3Error struct {
	Code    string
	Message string
}

// responseError turns a failed response into an *Error with a code. A
// HEAD response has no body, so its status says what it can.
func (b *s3) responseError(res *http.Response, op, key string) error {
	var e s3Error
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	xml.Unmarshal(body, &e)
	if e.Code == "" {
		switch res.StatusCode {
		case http.StatusNotFound:
			e.Code = "NoSuchKey"
			if key == "" {
				e.Code = "NoSuchBucket"
			}
		case http.StatusForbidden:
			e.Code = "AccessDenied"
		case http.StatusMovedPermanently:
			e.Code = "PermanentRedirect"
		}
	}
	what := fmt.Sprintf("%s %q in %s", op, key, b.Describe())
	if key == "" {
		what = op + " " + b.Describe()
	}
	msg := fmt.Sprintf("can't %s: HTTP %d %s", what, res.StatusCode, cmp.Or(e.Code, http.StatusText(res.StatusCode)))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	switch e.Code {
	case "NoSuchKey":
		return notFound(key)
	case "NoSuchBucket":
		return &Error{Code: SG005, Msg: msg, Fix: fmt.Sprintf("create the bucket %q in the service's console, or fix S3Config.Bucket", b.cfg.Bucket)}
	case "InvalidAccessKeyId", "SignatureDoesNotMatch", "AccessDenied", "InvalidToken", "ExpiredToken", "AuthorizationHeaderMalformed":
		fix := "check S3Config.AccessKeyID and SecretAccessKey, and that the key may read, write, delete and list objects in the bucket"
		if e.Code == "AuthorizationHeaderMalformed" {
			fix = "check S3Config.Region is the bucket's region (R2 uses \"auto\")"
		}
		return &Error{Code: SG004, Msg: msg, Fix: fix}
	case "PermanentRedirect":
		return &Error{Code: SG002, Msg: msg, Fix: "the bucket is in another region: set S3Config.Region to it"}
	}
	return &Error{Code: SG003, Msg: msg, Fix: "the storage service answered with an error; check its status, and S3Config"}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// Signature Version 4 (https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html).

// signV4 signs req: it sets X-Amz-Date, X-Amz-Content-Sha256 and
// Authorization, signing Host and every header already set.
func signV4(req *http.Request, payloadHash string, t time.Time, keyID, secret, region string) {
	t = t.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ",")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	slices.Sort(names)
	var canon bytes.Buffer
	for _, k := range names {
		canon.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signed := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(req.URL.Query()),
		canon.String(),
		signed,
		payloadHash,
	}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonicalRequest))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := hmacSHA256([]byte("AWS4"+secret), day)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+keyID+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// canonicalQuery is a query string as SigV4 signs it: sorted by name,
// each name and value escaped.
func canonicalQuery(q url.Values) string {
	names := make([]string, 0, len(q))
	for k := range q {
		names = append(names, k)
	}
	slices.Sort(names)
	var parts []string
	for _, k := range names {
		vs := slices.Clone(q[k])
		slices.Sort(vs)
		for _, v := range vs {
			parts = append(parts, escape(k, true)+"="+escape(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// escapePath escapes a key or bucket for a URL path, as SigV4 expects:
// everything but unreserved characters and "/".
func escapePath(s string) string { return escape(s, false) }

// escape percent-encodes every byte but A-Z a-z 0-9 - _ . ~, and "/"
// unless slash is true.
func escape(s string, slash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' || (c == '/' && !slash) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
