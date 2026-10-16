package loom

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/moomerman/loom/trace"
)

// Email (docs/auth.md). Loom sends no mail itself: the app gives it a
// Mailer, a one-method interface around whatever service it uses, and
// packages such as auth send through it from a background job, so a
// failed send is retried and shows in the jobs dashboard. Under `loom
// dev` an app without a mailer keeps what it sends instead, for `loom
// mail` and the devmail mailbox: an agent or a person can follow a
// sign-in link without a mail service.
//
//	app.UseMailer(postmark.New(token))           // the app's own adapter
//	err := app.Mailer().Send(ctx, loom.Mail{...}) // in a job's Perform

// Mail is an email to send.
type Mail struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`           // the plain-text body
	HTML    string `json:"html,omitempty"` // an HTML body, if any
}

// Mailer sends email. Implement it around the app's mail service (SMTP
// with net/smtp, or an HTTP API) and register it with App.UseMailer.
// Send is called from background jobs: an error retries the job.
type Mailer interface {
	Send(ctx context.Context, m Mail) error
}

// UseMailer sets the app's mailer.
func (a *App) UseMailer(m Mailer) {
	a.mailMu.Lock()
	defer a.mailMu.Unlock()
	a.mailer = m
}

// Mailer returns the app's mailer. Without one it is nil, except in dev
// (App.Dev), where it is an outbox that logs each mail and keeps the
// last ones for `loom mail`.
func (a *App) Mailer() Mailer {
	a.mailMu.Lock()
	defer a.mailMu.Unlock()
	if a.mailer == nil && a.dev {
		a.outbox.mu.Lock()
		a.outbox.logger = func(msg string, args ...any) {
			a.stream.Emit(context.Background(), trace.MailSent, append([]any{"msg", msg}, args...)...)
		}
		a.outbox.publish = func() { a.Broadcast(MailTopic, "sent", nil) }
		a.outbox.mu.Unlock()
		return &a.outbox
	}
	return a.mailer
}

// SentMail is a mail the dev outbox kept, as `loom mail --json` shows it.
type SentMail struct {
	ID   int       `json:"id"` // 1 for the first mail the app kept, counting up
	Time time.Time `json:"time"`
	Mail
}

// MailTopic is the PubSub topic the dev outbox broadcasts on, event
// "sent", each time it keeps a mail, and event "cleared" when
// ClearSentMail empties it: a dev mailbox page subscribes to it to show
// new mail as it arrives.
const MailTopic = "loom:mail"

const maxOutbox = 50

// outbox is the dev mailer: it logs mail and keeps the last few.
type outbox struct {
	mu      sync.Mutex
	list    []SentMail
	next    int // the last ID given
	logger  func(msg string, args ...any)
	publish func()
}

func (o *outbox) Send(ctx context.Context, m Mail) error {
	o.mu.Lock()
	o.next++
	o.list = append(o.list, SentMail{ID: o.next, Time: time.Now().UTC(), Mail: m})
	if len(o.list) > maxOutbox {
		o.list = o.list[len(o.list)-maxOutbox:]
	}
	logf, publish := o.logger, o.publish
	o.mu.Unlock()
	if logf != nil {
		logf("loom: mail kept for `loom mail` (no mailer in dev)", "to", m.To, "subject", m.Subject)
	}
	if publish != nil {
		publish()
	}
	return nil
}

func (o *outbox) all() []SentMail {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]SentMail{}, o.list...)
}

// SentMail returns the mail the dev outbox kept, oldest first: the last
// 50 the app sent while it ran in dev with no mailer. It is empty
// outside dev, and when the app has a mailer. The devmail package shows
// it in the browser; `loom mail` shows it in the terminal.
func (a *App) SentMail() []SentMail { return a.outbox.all() }

// ClearSentMail empties the dev outbox and broadcasts "cleared" on
// MailTopic. IDs keep counting up, so a link to a cleared mail never
// shows a newer one.
func (a *App) ClearSentMail() {
	a.outbox.mu.Lock()
	a.outbox.list = nil
	a.outbox.mu.Unlock()
	a.Broadcast(MailTopic, "cleared", nil)
}

// serveOutbox answers GET /_loom/dev/mail.
func (a *App) serveOutbox(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.outbox.all())
}

// StartupCheck adds a check that Run makes before it serves, and
// Validate (so loomtest.Mount) makes too, after Loom's own. A sub-app
// uses it for what can only be checked once the app is configured,
// such as auth needing a mailer: an error stops the app from starting.
func (a *App) StartupCheck(check func(ctx context.Context) error) {
	a.startupChecks = append(a.startupChecks, check)
}

// runStartupChecks runs the checks added with StartupCheck, in order.
func (a *App) runStartupChecks(ctx context.Context) error {
	for _, check := range a.startupChecks {
		if err := check(ctx); err != nil {
			return err
		}
	}
	return nil
}
