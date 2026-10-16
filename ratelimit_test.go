package loom_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/render"
)

// tryPage counts its "try" events; "look" is never limited.
type tryPage struct{ tries int }

func (p *tryPage) Mount(c *loom.Ctx) {}

func (p *tryPage) Events() loom.Events {
	return loom.Events{
		"try":  func(c *loom.Ctx, _ loom.Payload) { p.tries++ },
		"look": func(c *loom.Ctx, _ loom.Payload) {},
	}
}

func (p *tryPage) Assigns(c *loom.Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: strings.Repeat("|", p.tries), Events: []render.Event{{Name: "look"}, {Name: "try"}}}
}

func limitApp(db *loom.DB, opts ...loom.RouteOption) *loom.App {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if db != nil {
		app.UseDB(db)
	}
	app.Live("/", &tryPage{}, hxtest.Counter, opts...)
	return app
}

func TestRateLimitEvents(t *testing.T) {
	testRateLimitEvents(t, nil)
}

func TestRateLimitEventsPostgres(t *testing.T) {
	testRateLimitEvents(t, loomtest.DB(t, loomtest.Migrations("testdata/watch/migrations")))
}

// The sixth try by an email in a minute is refused, other emails and
// events aren't, and once the window has slid on it is allowed again.
func testRateLimitEvents(t *testing.T, db *loom.DB) {
	app := limitApp(db, loom.RateLimit(loom.Limit{Key: loom.ByPayload("email"), Max: 5, Per: time.Minute, On: []string{"try"}}))
	clock := loomtest.LimitClock(t, app)
	p := loomtest.Mount(t, app, "/")
	ada := loom.Payload{"email": "Ada@example.com "}
	for range 5 {
		p.Event("try", ada)
	}
	wait := p.AssertLimited("try", loom.Payload{"email": "ada@example.com"}) // trimmed and lowercased
	p.AssertText(".badge", "|||||")
	p.Event("try", loom.Payload{"email": "bob@example.com"})
	p.Event("look", ada)
	p.Event("try", nil) // no email: not counted
	// Six in this window: the last window's weight must fall to 4, a
	// third of the way into the next.
	if want := 80 * time.Second; wait != want {
		t.Errorf("retry after %s, want %s", wait, want)
	}
	clock.Advance(wait - time.Second)
	p.AssertLimited("try", ada)
	clock.Advance(time.Minute) // past this refusal's window too
	p.Event("try", ada)
}

// A group's limit counts page loads, events and handler requests across
// its routes; over it, a load is 429 with Retry-After and the error page,
// and an event is 429 with Retry-After and RT436.
func TestRateLimitGroup(t *testing.T) {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	clock := loomtest.LimitClock(t, app)
	g := app.Group(loom.RateLimit(loom.Limit{Key: loom.ByIP, Max: 4, Per: time.Hour}))
	g.Live("/", &tryPage{}, hxtest.Counter)
	g.Live("/two", &tryPage{}, hxtest.Counter)
	g.Handle("POST /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv := httptest.NewServer(app)
	defer srv.Close()

	get := func(path string) *http.Response {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := get("/")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	sid := strings.Split(strings.Split(string(body), `data-sid="`)[1], `"`)[0]
	if res, _ := http.Post(srv.URL+"/ping", "", nil); res.StatusCode != 200 {
		t.Fatalf("POST /ping = %d", res.StatusCode)
	}
	if res := get("/two"); res.StatusCode != 200 {
		t.Fatalf("GET /two = %d", res.StatusCode)
	}
	event := func() *http.Response {
		res, err := http.Post(srv.URL+"/_loom/event", "application/json", strings.NewReader(`{"sid":"`+sid+`","event":"try"}`))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := event(); res.StatusCode != 200 {
		t.Fatalf("event = %d", res.StatusCode)
	}
	res = event()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" || !strings.Contains(string(b), `"code":"RT436"`) {
		t.Fatalf("fifth attempt, an event: %d %q %s", res.StatusCode, res.Header.Get("Retry-After"), b)
	}
	res = get("/two")
	b, _ = io.ReadAll(res.Body)
	if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" || !strings.Contains(string(b), "Too many attempts") || !strings.Contains(string(b), "RT436") {
		t.Fatalf("a load over the limit: %d %q\n%s", res.StatusCode, res.Header.Get("Retry-After"), b)
	}
	if res, _ := http.Post(srv.URL+"/ping", "", nil); res.StatusCode != 429 {
		t.Fatalf("POST /ping over the limit = %d", res.StatusCode)
	}
	clock.Advance(2 * time.Hour)
	if res := get("/"); res.StatusCode != 200 {
		t.Fatalf("GET / two hours later = %d", res.StatusCode)
	}
}

// ByUser counts only signed-in attempts.
func TestRateLimitByUser(t *testing.T) {
	app := limitApp(nil, loom.RateLimit(loom.Limit{Key: loom.ByUser, Max: 1, Per: time.Minute, On: []string{"try"}}))
	loomtest.LimitClock(t, app)
	p := loomtest.Mount(t, app, "/")
	p.Event("try", nil)
	p.Event("try", nil)
}

func TestRateLimitChecked(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit loom.Limit
		want  string
	}{
		{"no key", loom.Limit{Max: 1, Per: time.Minute}, "has no Key"},
		{"no max", loom.Limit{Key: loom.ByIP, Per: time.Minute}, "allows 0 attempts"},
		{"no window", loom.Limit{Key: loom.ByIP, Max: 1}, "has a window of 0s"},
		{"unknown event", loom.Limit{Key: loom.ByIP, Max: 1, Per: time.Minute, On: []string{"tyr"}}, `counts event "tyr", which *loom_test.tryPage doesn't handle`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				e, ok := recover().(*loom.Error)
				if !ok || e.Code != loom.RT437 || !strings.Contains(e.Msg, tc.want) || !strings.Contains(e.Where, "ratelimit_test.go") {
					t.Fatalf("got %v, want RT437 %q at ratelimit_test.go", e, tc.want)
				}
			}()
			limitApp(nil, loom.RateLimit(tc.limit))
		})
	}
}

// The manifest and loom routes show each route's limits.
func TestRateLimitManifest(t *testing.T) {
	app := limitApp(nil, loom.RateLimit(loom.Limit{Key: loom.ByPayload("email"), Max: 5, Per: time.Minute, On: []string{"try"}}))
	m := app.Manifest()
	if got := m.Routes[0].RateLimit; len(got) != 1 || got[0] != "5 per minute by email on try" {
		t.Errorf("manifest rate_limit = %q", got)
	}
	if !strings.Contains(m.Text(), "rate limit: 5 per minute by email on try") {
		t.Errorf("manifest text:\n%s", m.Text())
	}
}
