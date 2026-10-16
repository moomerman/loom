package main

import (
	"fmt"
	"io"

	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/compiler/diag"
)

// buildOutput is the JSON form of `loom build --json`.
type buildOutput struct {
	Written     []string          `json:"written"`
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
}

// runBuild implements `loom build`: check templates and write the
// generated Go next to each one. It exits like `loom check`.
func runBuild(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("build", "build [--json] [PATH ...]", "print the result as JSON", stderr)
	if !fs.parse(args, 0, -1) {
		return 2
	}
	asJSON := fs.JSON
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	res, written, err := compiler.Build(paths...)
	if err != nil {
		fmt.Fprintf(stderr, "loom build: %v\n", err)
		return 2
	}

	if *asJSON {
		writeJSON(stdout, buildOutput{Written: append([]string{}, written...), Diagnostics: diagnostics(res.Diagnostics)})
	} else {
		printDiagnostics(stdout, res)
		if !res.Diagnostics.HasErrors() {
			for _, w := range written {
				fmt.Fprintf(stdout, "wrote %s\n", w)
			}
			fmt.Fprintf(stdout, "ok: %d %s built, %d %s written\n", res.Checked, plural(res.Checked, "template"), len(written), plural(len(written), "file"))
		}
	}
	if res.Diagnostics.HasErrors() {
		return 1
	}
	return 0
}
