package loom_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/loomtest"
)

// Mail: no mailer is nil, except in dev, where the outbox keeps mail for
// `loom mail`; a mailer the app sets is used everywhere.
func TestMailer(t *testing.T) {
	if quietApp().Mailer() != nil {
		t.Fatal("a new app has a mailer")
	}
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: loom.DevOn})
	m := loom.Mail{To: "ada@example.com", Subject: "Hi", Text: "Open https://app.test/x\n"}
	if err := app.Mailer().Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()
	var kept []loom.SentMail
	res, err := http.Get(srv.URL + "/_loom/dev/mail")
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(res.Body).Decode(&kept)
	res.Body.Close()
	if len(kept) != 1 || kept[0].Mail != m || kept[0].Time.IsZero() {
		t.Fatalf("/_loom/dev/mail: %+v", kept)
	}

	box := loomtest.Mail(t, app)
	app.Mailer().Send(context.Background(), m)
	if box.Last(t) != m || box.Link(t) != "/x" {
		t.Errorf("mailbox: %+v, link %q", box.Mail(), box.Link(t))
	}
}

// StartupCheck: Validate (and Run) run the checks, after Loom's own.
func TestStartupCheck(t *testing.T) {
	app := quietApp()
	ran := 0
	app.StartupCheck(func(ctx context.Context) error { ran++; return nil })
	boom := errors.New("boom")
	app.StartupCheck(func(ctx context.Context) error { return boom })
	if err := app.Validate(context.Background()); err != boom || ran != 1 {
		t.Errorf("Validate = %v after %d checks, want boom after 1", err, ran)
	}
}
