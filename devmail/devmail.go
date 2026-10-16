// Package devmail is a mailbox for development: it shows the mail the
// app sent while it ran under `loom dev` with no mailer, such as auth's
// sign-in links and password resets, so a person can follow them in the
// browser the way an agent does with `loom mail`. Mount it with one line:
//
//	app.Mount("/dev/mail", devmail.New())
//
// It is only ever served in dev. Its pages answer 404 unless the app
// runs in dev (App.Dev, which `loom dev` sets), and 403 to other
// machines, so the line can stay in the app for production: there the
// mailbox doesn't exist, and the app's real mailer sends the mail. New
// mail appears as it is sent, without a reload.
//
// It reads the app's dev outbox (App.SentMail), the same mail `loom
// mail` shows, which keeps the last 50 mails in memory: a restart, as
// `loom dev` does on every change, empties it.
//
//loom:package
package devmail

//go:generate go run github.com/moomerman/loom/cmd/loom build .

import (
	"embed"
	"io/fs"

	"github.com/moomerman/loom"
)

// App is the mountable mailbox. Create it with New and mount it with
// App.Mount or Group.Mount.
type App struct {
	app *loom.App // set by Routes
}

// New returns the mailbox.
func New() *App { return &App{} }

// RequiresAuth is false: the mailbox is public by design, since the
// person reading it is usually not signed in yet. loom.DevOnly keeps it
// to dev and to the local machine instead.
func (a *App) RequiresAuth() bool { return false }

// files holds the favicon, linked with asset("icon.svg").
//
//go:embed assets
var files embed.FS

// Routes registers the mailbox, relative to the mount prefix: the newest
// mail at "/", and each mail at "/{id}". Both are the same page, a list
// beside the open mail. The group is DevOnly, whatever the host's
// group is, so mounting the mailbox can never publish it.
func (a *App) Routes(g *loom.Group) {
	a.app = g.App()
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	g = g.Group(
		loom.DevOnly(),
		loom.Assets(assets),
		loom.RootLayout(Root, func(c *loom.Ctx) RootAssigns {
			return RootAssigns{Title: c.TitleOr("Mail")}
		}),
	)
	g.Live("/", &MailboxPage{app: a}, Mailbox)
	g.Live("/{id}", &MailboxPage{app: a}, Mailbox)
}
