package loomtest

import (
	"context"
	"net/url"
	"regexp"
	"sync"
	"testing"

	"github.com/moomerman/loom"
)

// Mailbox is a loom.Mailer that keeps the mail sent, for tests of code
// that sends mail, such as auth's sign-in links:
//
//	box := loomtest.Mail(t, app)
//	p.Submit("form", loom.Payload{"email": "ada@example.com"}) // enqueues the mail job
//	q.Drain(t)                                                 // which sends it
//	m := box.Last(t)                                           // m.To, m.Subject, m.Text
//	p = loomtest.Mount(t, app, box.Link(t))                    // follow its link
type Mailbox struct {
	mu   sync.Mutex
	mail []loom.Mail
}

// Mail returns a Mailbox and makes it app's mailer.
func Mail(t testing.TB, app *loom.App) *Mailbox {
	b := &Mailbox{}
	app.UseMailer(b)
	return b
}

// Send keeps m.
func (b *Mailbox) Send(ctx context.Context, m loom.Mail) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mail = append(b.mail, m)
	return nil
}

// Mail returns the mail sent so far, oldest first.
func (b *Mailbox) Mail() []loom.Mail {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]loom.Mail{}, b.mail...)
}

// Last returns the last mail sent, failing the test if there is none.
func (b *Mailbox) Last(t testing.TB) loom.Mail {
	t.Helper()
	mail := b.Mail()
	if len(mail) == 0 {
		t.Fatalf("no mail was sent (a mail sent by a job is sent when the job runs: drain the loomtest.Jobs queue first)")
	}
	return mail[len(mail)-1]
}

var linkRE = regexp.MustCompile(`https?://[^\s<>"]+`)

// Link returns the path, with its query, of the first link in the text
// of the last mail sent, ready for Mount. It fails the test if there is
// no mail or the mail has no link.
func (b *Mailbox) Link(t testing.TB) string {
	t.Helper()
	m := b.Last(t)
	raw := linkRE.FindString(m.Text)
	if raw == "" {
		t.Fatalf("the last mail, to %s, %q, has no link:\n%s", m.To, m.Subject, m.Text)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("the last mail's link %q: %v", raw, err)
	}
	return u.RequestURI()
}
