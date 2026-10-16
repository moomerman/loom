package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/kit"
	"github.com/moomerman/loom/kit/manage"
)

// componentsOutput is the JSON form of `loom components add --json`.
type componentsOutput struct {
	Results     []manage.Result   `json:"results"`
	Written     []string          `json:"written"`     // generated Go files
	Diagnostics []diag.Diagnostic `json:"diagnostics"` // from building the directory
}

// runComponents implements `loom components`: list the starter kit and
// add components to an app.
func runComponents(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "add" {
		return componentsAdd(args[1:], stdout, stderr)
	}
	fs := newCommand("components", "components [--json] | add NAME...", "print JSON", stderr)
	if !fs.parse(args, 0, 0) {
		return 2
	}
	asJSON := fs.JSON
	cat, err := manage.Catalog(kit.Default)
	if err != nil {
		fmt.Fprintf(stderr, "loom components: %v\n", err)
		return 2
	}
	if *asJSON {
		writeJSON(stdout, cat)
		return 0
	}
	for _, c := range cat {
		fmt.Fprintf(stdout, "%-12s %s\n", c.Name, c.Summary)
		for _, d := range c.Components {
			fmt.Fprintf(stdout, "    %s(%s)\n", d.Name, signature(d.Params))
		}
		for _, d := range c.Behaviors {
			emits := ""
			if len(d.Emits) > 0 {
				emits = " emits (" + strings.Join(d.Emits, ", ") + ")"
			}
			fmt.Fprintf(stdout, "    behavior %s(%s)%s\n", d.Name, signature(d.Params), emits)
		}
	}
	fmt.Fprint(stdout, "\nAdd with: loom components add NAME...\n")
	return 0
}

// signature writes parameters as in a declaration.
func signature(ps []manage.Param) string {
	var params []string
	for _, p := range ps {
		s := p.Name + " " + p.Type
		if strings.HasPrefix(p.Type, ":") { // an enum, written as in the signature
			s = p.Name + ": " + p.Type
		}
		if p.Default != "" {
			s += " = " + p.Default
		}
		params = append(params, s)
	}
	return strings.Join(params, ", ")
}

// componentsAdd runs `loom components add`, then builds the components
// directory so the copies are ready to import. It exits 1 if the build
// failed.
func componentsAdd(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("components add", "components add [--json] [--dir DIR] NAME...", "print JSON", stderr)
	dir := fs.String("dir", "components", "the app's components directory")
	if !fs.parse(args, 0, -1) {
		return 2
	}
	asJSON := fs.JSON
	results, err := manage.Add(kit.Default, *dir, fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "loom components add: %v\n", err)
		return 2
	}
	res, written, err := compiler.Build(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "loom components add: %v\n", err)
		return 2
	}
	if *asJSON {
		writeJSON(stdout, componentsOutput{Results: results, Written: append([]string{}, written...), Diagnostics: diagnostics(res.Diagnostics)})
	} else {
		for _, r := range results {
			switch r.Status {
			case manage.Added:
				fmt.Fprintf(stdout, "added %s: %s\n", r.Name, strings.Join(r.Files, ", "))
			case manage.TokensNew:
				fmt.Fprintf(stdout, "wrote the kit's design tokens: %s\n", r.File)
			}
		}
		printDiagnostics(stdout, res)
	}
	if res.Diagnostics.HasErrors() {
		return 1
	}
	return 0
}
