package loom

import (
	"net/http"
	"net/http/cookiejar"
	"os"
	"testing"
)

// TestMain gives the default HTTP client a cookie jar, so the tests'
// requests act as one browser: a page load sets BrowserCookie and the
// stream, events and navigations that follow carry it (browser.go).
// Tests of another browser use a client of their own.
func TestMain(m *testing.M) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}
	http.DefaultClient.Jar = jar
	os.Exit(m.Run())
}
