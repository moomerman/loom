// Command tally runs Tally, the shared-expenses example, on a Postgres
// database:
//
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/tally go run ./cmd/loom migrate --dir examples/tally/migrations
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/tally go run ./examples/tally/cmd/tally
package main

import (
	"log"
	"os"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/tally"
)

func main() {
	addr := ":4000"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("tally: set DATABASE_URL to a Postgres database, e.g. postgres://postgres:postgres@localhost:5432/tally, and create its tables with `loom migrate --dir examples/tally/migrations`")
	}
	app := loom.New(loom.Config{DB: url})
	tally.Setup(app)
	if err := app.Run(addr); err != nil { // nil after a graceful shutdown
		log.Fatal(err)
	}
}
