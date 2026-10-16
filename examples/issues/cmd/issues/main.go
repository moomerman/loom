// Command issues runs the Loom issue tracker example on a Postgres
// database. Sign in as ada@example.com with the password loom-issues;
// mail, such as sign-in links, is logged rather than sent:
//
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/issues go run ./cmd/loom migrate --dir examples/issues/migrations
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/issues go run ./examples/issues/cmd/issues
package main

import (
	"log"
	"os"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/issues"
)

func main() {
	addr := ":4000"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("issues: set DATABASE_URL to a Postgres database, e.g. postgres://postgres:postgres@localhost:5432/issues, and create its tables with `loom migrate --dir examples/issues/migrations`")
	}
	base, secret := os.Getenv("BASE_URL"), os.Getenv("LOOM_SECRET")
	if base == "" {
		base = "http://localhost" + addr
		if secret == "" {
			// A fixed secret for trying the example on localhost only:
			// a deployment passes its own (docs/deploy.md).
			secret = "issues-example-secret-for-localhost-only"
		}
	}
	app := loom.New(loom.Config{DB: url, BaseURL: base, Secret: secret})
	if !app.Dev() { // under loom dev, mail is kept for `loom mail` and /dev/mail
		app.UseMailer(issues.LogMailer{Logger: app.Logger()})
	}
	issues.Setup(app)
	if err := app.Run(addr); err != nil {
		log.Fatal(err)
	}
}
