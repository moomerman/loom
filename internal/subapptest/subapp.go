// Package subapptest is a tiny sub-app for testing loom's Mount: two
// pages that link to each other with path, a root layout of its own that
// links an embedded stylesheet with asset, and an optional auth
// requirement. Tests mount it at two prefixes in one app.
//
//loom:package
package subapptest

//go:generate go run github.com/moomerman/loom/cmd/loom build .

import (
	"embed"
	"io/fs"

	"github.com/moomerman/loom"
)

//go:embed assets
var files embed.FS

// App is the sub-app. With NeedsAuth it must be mounted behind loom.Auth.
type App struct {
	NeedsAuth bool
}

// RequiresAuth reports whether the sub-app must be mounted behind an
// auth check.
func (a *App) RequiresAuth() bool { return a.NeedsAuth }

// Routes serves the pages in the sub-app's own root layout, with its
// stylesheet embedded.
func (a *App) Routes(g *loom.Group) {
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	g = g.Group(loom.Assets(assets), loom.RootLayout(Root, func(c *loom.Ctx) RootAssigns {
		return RootAssigns{Title: c.TitleOr("Sub")}
	}))
	g.Live("/", &IndexPage{}, Index)
	g.Live("/items/{id}", &ItemPage{}, Item)
}

// Entry is a listed item.
type Entry struct {
	ID   string
	Name string
}

// IndexPage lists the items.
type IndexPage struct{}

// Mount has nothing to load.
func (p *IndexPage) Mount(c *loom.Ctx) {}

// Assigns lists two items.
func (p *IndexPage) Assigns(c *loom.Ctx) IndexAssigns {
	return IndexAssigns{Items: []Entry{{ID: "1", Name: "One"}, {ID: "2", Name: "Two"}}}
}

// ItemPage shows one item, with c.Path computing a link in Go.
type ItemPage struct{ id string }

// Mount reads the item id from the path.
func (p *ItemPage) Mount(c *loom.Ctx) { p.id = c.Request.PathValue("id") }

// Assigns shows the item.
func (p *ItemPage) Assigns(c *loom.Ctx) ItemAssigns {
	return ItemAssigns{ID: p.id, Next: c.Path("/items/" + p.id + "/next")}
}
