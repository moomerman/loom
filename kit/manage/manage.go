// Package manage lists the starter kit's components and copies them into
// an app: `loom components` and `loom components add` (see
// docs/components.md).
//
// An app owns its copies and may edit them. Nothing records where a copy
// came from: to pick up a newer kit component, diff the copy against the
// kit's file and apply what you want.
package manage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/parser"
	"github.com/moomerman/loom/compiler/style"
	"github.com/moomerman/loom/kit"
)

// Component describes a kit component for `loom components`.
type Component struct {
	Name       string   `json:"name"`
	Summary    string   `json:"summary"`
	Files      []string `json:"files"`
	Components []Decl   `json:"components"` // the components the file declares
	Behaviors  []Decl   `json:"behaviors"`  // the behaviors it declares
}

// Decl is a component or behavior declaration: its name and parameters,
// and for a behavior the events it emits.
type Decl struct {
	Name   string   `json:"name"`
	Params []Param  `json:"params"`
	Emits  []string `json:"emits,omitempty"`
}

// Param is a parameter as written in the signature.
type Param struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Default  string `json:"default,omitempty"`
	Required bool   `json:"required"`
}

// Catalog describes every component of k.
func Catalog(k kit.Kit) ([]Component, error) {
	var out []Component
	for _, e := range k.Catalog {
		src, err := k.Source(e, e.File())
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseString(e.File(), string(src))
		if err != nil {
			return nil, fmt.Errorf("kit %s: %v", e.Name, err)
		}
		c := Component{Name: e.Name, Summary: e.Summary, Files: e.Files(), Behaviors: []Decl{}}
		for _, decl := range f.Components {
			c.Components = append(c.Components, Decl{Name: decl.Name, Params: params(decl.Params)})
		}
		for _, b := range f.Behaviors {
			d := Decl{Name: b.Name, Params: params(b.Params)}
			for _, em := range b.Emits {
				d.Emits = append(d.Emits, em.Name)
			}
			c.Behaviors = append(c.Behaviors, d)
		}
		out = append(out, c)
	}
	return out, nil
}

func params(ps []*ast.Param) []Param {
	out := []Param{}
	for _, p := range ps {
		prm := Param{Name: p.Name, Type: ast.TypeString(p.Type)}
		if p.Default != nil {
			prm.Default = ast.ExprString(p.Default)
		}
		named, _ := p.Type.(*ast.NamedType)
		optional := named != nil && (named.Name == "slot" || named.Name == "event")
		prm.Required = p.Default == nil && !optional
		out = append(out, prm)
	}
	return out
}

// Result is what add did: one component copied, or the kit's tokens.toml.
type Result struct {
	Name   string   `json:"name"`
	File   string   `json:"file"`   // the .hx file
	Files  []string `json:"files"`  // every file written, the .hx file first
	Status string   `json:"status"` // see the Status constants
}

// The statuses of a Result.
const (
	Added     = "added"  // copied into the app
	TokensNew = "tokens" // the kit's tokens.toml was copied (Name is "tokens.toml")
)

// Add copies components from k into dir. If no tokens.toml applies to
// dir, it copies the kit's next to dir. It refuses to overwrite a file
// that is already there, so it never touches an app's edited copy.
func Add(k kit.Kit, dir string, names []string) ([]Result, error) {
	if len(names) == 0 {
		return nil, errors.New("name the components to add, e.g. loom components add button")
	}
	var entries []kit.Entry
	for _, name := range names {
		e, ok := k.Lookup(name)
		if !ok {
			return nil, unknown(k, name)
		}
		for _, file := range e.Files() {
			if p := filepath.Join(dir, file); exists(p) {
				return nil, fmt.Errorf("%s already exists; the app owns its copy of %s, so edit it, or move it away to add the kit's again", p, e.Name)
			}
		}
		entries = append(entries, e)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var results []Result
	for _, e := range entries {
		r := Result{Name: e.Name, File: filepath.Join(dir, e.File()), Status: Added}
		for _, file := range e.Files() {
			src, err := k.Source(e, file)
			if err != nil {
				return nil, err
			}
			p := filepath.Join(dir, file)
			if err := os.WriteFile(p, src, 0o644); err != nil {
				return nil, err
			}
			r.Files = append(r.Files, p)
		}
		results = append(results, r)
	}
	if style.Find(dir) == "" {
		tokens, err := k.Tokens()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(filepath.Dir(filepath.Clean(dir)), "tokens.toml")
		if err := os.WriteFile(path, tokens, 0o644); err != nil {
			return nil, err
		}
		results = append(results, Result{Name: "tokens.toml", File: path, Files: []string{path}, Status: TokensNew})
	}
	return results, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func unknown(k kit.Kit, name string) error {
	var names []string
	for _, e := range k.Catalog {
		names = append(names, e.Name)
	}
	return fmt.Errorf("the kit has no component %q; it has %s", name, strings.Join(names, ", "))
}
