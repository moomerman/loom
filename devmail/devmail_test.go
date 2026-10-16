package devmail_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/devmail"
	"github.com/moomerman/loom/loomtest"
)

// mailbox returns an app in dev with the mailbox at /dev/mail.
func mailbox(t *testing.T) *loom.App { return mailboxIn(loom.DevOn) }

func mailboxIn(dev loom.DevMode) *loom.App {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: dev})
	app.Mount("/dev/mail", devmail.New())
	return app
}

func send(t *testing.T, app *loom.App, m loom.Mail) {
	t.Helper()
	if err := app.Mailer().Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func TestMailbox(t *testing.T) {
	app := mailbox(t)
	p := loomtest.Mount(t, app, "/dev/mail")
	p.Listen(app.PubSub())
	p.AssertText("main h2", "No mail yet")
	if p.Title() != "Mail" || !strings.Contains(p.Document(), "/dev/mail/_assets/icon.") {
		t.Fatalf("title %q, document:\n%s", p.Title(), p.Document())
	}
	p.AssertAccessible()

	// Live: mail appears as it is sent, and "/" opens the newest.
	send(t, app, loom.Mail{To: "ada@example.com", Subject: "Sign in to Issues", Text: "Sign in by opening this link:\n\nhttp://localhost:4000/auth/link/abc.\n"})
	p.AssertHTML("ol .item", 1)
	p.AssertText("h1", "Sign in to Issues")
	p.AssertHTML(`.links a.follow[href="http://localhost:4000/auth/link/abc"]`, 1) // without the full stop
	p.AssertHTML(`pre.body a[href="http://localhost:4000/auth/link/abc"]`, 1)
	p.AssertHTML("iframe", 0)
	if p.Title() != "Sign in to Issues" {
		t.Errorf("title %q", p.Title())
	}
	p.AssertAccessible()

	send(t, app, loom.Mail{To: "bob@example.com", Subject: "Reset your password", Text: "Open https://app.test/reset/xyz", HTML: `<p><a href="https://app.test/reset/xyz">Reset</a></p>`})
	p.AssertHTML("ol .item", 2)
	p.AssertText("h1", "Reset your password")
	p.AssertHTML(`a.current[href="/dev/mail/2"]`, 1)
	p.AssertHTML(".links .link", 1) // the same link in text and HTML, once
	p.AssertHTML(`iframe[sandbox=""]`, 1)
	p.AssertAccessible()

	// A mail's own page keeps it open as more arrive.
	first := loomtest.Mount(t, app, "/dev/mail/1")
	first.Listen(app.PubSub())
	first.AssertText("h1", "Sign in to Issues")
	send(t, app, loom.Mail{To: "cy@example.com", Subject: ""})
	first.AssertHTML("ol .item", 3)
	first.AssertText("h1", "Sign in to Issues")
	p.AssertText("h1", "(no subject)")

	// Clearing empties every open mailbox; a cleared mail's page says so.
	p.Event("clear", nil)
	p.AssertHTML("ol", 0)
	first.AssertText("main h2", "Mail 1 is gone")
	if len(app.SentMail()) != 0 {
		t.Fatalf("SentMail after clear: %+v", app.SentMail())
	}
	send(t, app, loom.Mail{To: "dee@example.com", Subject: "Hi"})
	if got := app.SentMail(); len(got) != 1 || got[0].ID != 4 {
		t.Fatalf("IDs restarted after clear: %+v", got)
	}
}

// The mailbox is served only in dev, and only to this machine.
func TestDevOnly(t *testing.T) {
	dev := httptest.NewServer(mailbox(t))
	defer dev.Close()
	live := httptest.NewServer(mailboxIn(loom.DevOff))
	defer live.Close()
	get := func(srv *httptest.Server, forwarded string) int {
		req, _ := http.NewRequest("GET", srv.URL+"/dev/mail", nil)
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := get(dev, ""); code != http.StatusOK {
		t.Errorf("in dev: %d, want 200", code)
	}
	if code := get(dev, "203.0.113.9"); code != http.StatusForbidden {
		t.Errorf("from another machine: %d, want 403", code)
	}
	if code := get(live, ""); code != http.StatusNotFound {
		t.Errorf("outside dev: %d, want 404", code)
	}
}
