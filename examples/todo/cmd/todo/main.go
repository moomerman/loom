// Command todo runs the Loom todo example on a Postgres database:
//
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/todo go run ./cmd/loom migrate --dir examples/todo/migrations
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/todo go run ./examples/todo/cmd/todo
package main

import (
	"log"
	"os"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/todo"
)

func main() {
	addr := ":4000"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("todo: set DATABASE_URL to a Postgres database, e.g. postgres://postgres:postgres@localhost:5432/todo, and create its tables with `loom migrate --dir examples/todo/migrations`")
	}
	app := loom.New(loom.Config{DB: url})
	todo.Setup(app)
	if err := app.Run(addr); err != nil {
		log.Fatal(err)
	}
}
