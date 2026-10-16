package main

import (
	"fmt"
	"io"

	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/compiler/diag"
)

// runCheck implements `loom check`. It exits 0 when every template is
// valid (warnings allowed), 1 when there are errors, and 2 on usage or
// I/O errors.
func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("check", "check [--json] [PATH ...]", "print diagnostics as a JSON array", stderr)
	if !fs.parse(args, 0, -1) {
		return 2
	}
	asJSON := fs.JSON
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	res, err := compiler.Check(paths...)
	if err != nil {
		fmt.Fprintf(stderr, "loom check: %v\n", err)
		return 2
	}

	if *asJSON {
		writeJSON(stdout, diagnostics(res.Diagnostics))
	} else {
		printDiagnostics(stdout, res)
		if !res.Diagnostics.HasErrors() && res.Checked > 0 {
			fmt.Fprintf(stdout, "ok: %d %s checked\n", res.Checked, plural(res.Checked, "template"))
		}
	}
	if res.Diagnostics.HasErrors() {
		return 1
	}
	return 0
}

// diagnostics converts errors to their JSON form; never nil, so an empty
// list encodes as [].
func diagnostics(errs diag.List) []diag.Diagnostic {
	out := make([]diag.Diagnostic, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Diagnostic())
	}
	return out
}

// printDiagnostics prints errors for people, followed by a summary line.
func printDiagnostics(w io.Writer, res *compiler.Result) {
	for _, d := range res.Diagnostics {
		fmt.Fprintln(w, d.Error())
	}
	errs, warns := 0, 0
	for _, d := range res.Diagnostics {
		if d.Warning {
			warns++
		} else {
			errs++
		}
	}
	switch {
	case res.Checked == 0:
		fmt.Fprintln(w, "no .hx templates found")
	case errs > 0:
		fmt.Fprintf(w, "%d %s in %d %s checked\n", errs, plural(errs, "error"), res.Checked, plural(res.Checked, "template"))
	case warns > 0:
		fmt.Fprintf(w, "%d %s\n", warns, plural(warns, "warning"))
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
