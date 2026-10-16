package issues

import (
	"context"
	"log/slog"

	"github.com/moomerman/loom"
)

// LogMailer is a loom.Mailer that logs each mail instead of sending it,
// for running the example without a mail service: sign-in and reset
// links appear in the log. A real app wraps its mail service's API (or
// net/smtp) in the same one method.
type LogMailer struct{ Logger *slog.Logger }

func (m LogMailer) Send(ctx context.Context, mail loom.Mail) error {
	m.Logger.Info("issues: mail (not sent, LogMailer)", "to", mail.To, "subject", mail.Subject, "text", mail.Text)
	return nil
}
