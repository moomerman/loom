package storage

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The examples in AWS's S3 documentation, "Signature Calculations for
// the Authorization Header: Transferring Payload in a Single Chunk".
const (
	exampleKeyID  = "AKIAIOSFODNN7EXAMPLE"
	exampleSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

var exampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func TestSignV4(t *testing.T) {
	cases := []struct {
		name, method, url string
		header            http.Header
		payload           string
		want              string
	}{
		{
			name:    "GET Object",
			method:  "GET",
			url:     "https://examplebucket.s3.amazonaws.com/test.txt",
			header:  http.Header{"Range": {"bytes=0-9"}},
			payload: emptyHash,
			want:    "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
		},
		{
			name:    "GET Bucket Lifecycle",
			method:  "GET",
			url:     "https://examplebucket.s3.amazonaws.com/?lifecycle",
			payload: emptyHash,
			want:    "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543",
		},
		{
			name:    "Get Bucket (List Objects)",
			method:  "GET",
			url:     "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J",
			payload: emptyHash,
			want:    "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7",
		},
		{
			name:    "PUT Object",
			method:  "PUT",
			url:     "https://examplebucket.s3.amazonaws.com/test$file.text",
			header:  http.Header{"Date": {"Fri, 24 May 2013 00:00:00 GMT"}, "X-Amz-Storage-Class": {"REDUCED_REDUNDANCY"}},
			payload: "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072", // "Welcome to Amazon S3."
			want:    "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class, Signature=98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd",
		},
	}
	for _, c := range cases {
		u := c.url
		if p, err := url.Parse(u); err == nil && p.Path != "/" {
			// Escape the path as the client does.
			u = p.Scheme + "://" + p.Host + "/" + escapePath(strings.TrimPrefix(p.Path, "/"))
			if p.RawQuery != "" {
				u += "?" + p.RawQuery
			}
		}
		req, err := http.NewRequest(c.method, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range c.header {
			req.Header[k] = v
		}
		signV4(req, c.payload, exampleTime, exampleKeyID, exampleSecret, "us-east-1")
		if got := req.Header.Get("Authorization"); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestEscape(t *testing.T) {
	for in, want := range map[string]string{
		"a/b c":      "a/b%20c",
		"日本":         "%E6%97%A5%E6%9C%AC",
		"a+b=c&d":    "a%2Bb%3Dc%26d",
		"-_.~AZaz09": "-_.~AZaz09",
		"$":          "%24",
	} {
		if got := escapePath(in); got != want {
			t.Errorf("escapePath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := canonicalQuery(url.Values{"prefix": {"a/b c"}, "list-type": {"2"}, "lifecycle": {""}}); got != "lifecycle=&list-type=2&prefix=a%2Fb%20c" {
		t.Errorf("canonicalQuery = %q", got)
	}
}
