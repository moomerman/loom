// Package notestest is a sub-app that keeps data in the app's database,
// for testing sub-app migrations: `loom db install` and the check that
// they are applied (RT433).
package notestest

import (
	"embed"
	"io/fs"

	"github.com/moomerman/loom"
)

//go:embed migrations
var migrations embed.FS

// App is the sub-app. It has no pages: only its tables matter here.
type App struct{}

func (*App) Routes(g *loom.Group) {}

// Migrations returns the package's migrations.
func (*App) Migrations() fs.FS {
	m, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	}
	return m
}
