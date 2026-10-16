// Package kit is Loom's starter kit: components an app copies into its own
// components/ directory with `loom components add`, and then owns. They
// are compiled and tested here like any template package, but apps never
// import this package; see docs/components.md.
package kit

import (
	"embed"
	"fmt"
	"io/fs"
)

// Files holds every component's files (its .hx file and the modules of
// the behaviors it declares) and the kit's tokens.toml.
//
//go:embed *.hx *.js tokens.toml
var Files embed.FS

// Entry describes a component of the kit.
type Entry struct {
	Name    string // as added: loom components add button
	Summary string
	// Modules are the ES modules of the behaviors the file declares,
	// copied with it.
	Modules []string
}

// File is the entry's template file name in the kit and in an app.
func (e Entry) File() string { return e.Name + ".hx" }

// Files lists the entry's files: its template, then its modules.
func (e Entry) Files() []string { return append([]string{e.File()}, e.Modules...) }

// Catalog lists the kit's components.
var Catalog = []Entry{
	{"alert", "A message with a tone: info, success, warning or danger", nil},
	{"badge", "A small label with a tone", nil},
	{"button", "A button with variants, sizes, a busy state and a click event", nil},
	{"card", "A surface with an optional title", nil},
	{"checkbox", "A labelled checkbox with a change event", nil},
	{"dialog", "A modal dialog that keeps focus inside, with a title, content, actions and a close event", []string{"dialog.js"}},
	{"dropdown", "A button that opens a listbox of options, driven by the keyboard (Dropdown and DropdownOption)", []string{"dropdown.js"}},
	{"empty_state", "What to show when a list is empty", nil},
	{"form_field", "A label, hint and error around any control", nil},
	{"input", "A labelled text input (text, email, password, search, number, url or date) with hint and error", nil},
	{"link", "A link, inline or in navigation, with the current page marked", nil},
	{"select", "A labelled select; options go in its content", nil},
	{"spinner", "A loading indicator with an accessible label", nil},
	{"table", "A table with a caption, header row and body", nil},
	{"tabs", "A tab list and tabs, with arrow keys between tabs (Tabs and Tab)", []string{"tabs.js"}},
	{"textarea", "A labelled multi-line text input", nil},
}

// Kit is a set of components: the embedded kit, or a test double.
type Kit struct {
	FS      fs.FS
	Catalog []Entry
}

// Default is the embedded starter kit.
var Default = Kit{FS: Files, Catalog: Catalog}

// Lookup returns the entry called name.
func (k Kit) Lookup(name string) (Entry, bool) {
	for _, e := range k.Catalog {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Source returns one of a component's files.
func (k Kit) Source(e Entry, file string) ([]byte, error) {
	data, err := fs.ReadFile(k.FS, file)
	if err != nil {
		return nil, fmt.Errorf("the kit has no %s in %s: %w", file, e.Name, err)
	}
	return data, nil
}

// Tokens returns the kit's tokens.toml.
func (k Kit) Tokens() ([]byte, error) { return fs.ReadFile(k.FS, "tokens.toml") }
