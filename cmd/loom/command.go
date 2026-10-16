package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
)

// command is what every loom command shares: a flag set with --json,
// a usage line printed on a usage error, and flags accepted before or
// after the arguments (`loom inspect SID --json`). Commands exit 0 when
// done, 1 for a problem they report (as JSON with --json), and 2 for a
// usage or connection error. A new command starts with newCommand and
// parse, and prints JSON with writeJSON, so it behaves like the rest.
type command struct {
	*flag.FlagSet
	JSON *bool
}

// newCommand makes a command's flags: usage is the line after
// "usage: loom ", and jsonHelp says what --json prints.
func newCommand(name, usage, jsonHelp string, stderr io.Writer) *command {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintf(stderr, "usage: loom %s\n", usage); fs.PrintDefaults() }
	return &command{FlagSet: fs, JSON: fs.Bool("json", false, jsonHelp)}
}

// parse parses args and checks there are min to max arguments (max -1
// for any number), printing the usage if not. It reports whether the
// command should go on; if not, it exits 2.
func (c *command) parse(args []string, min, max int) bool {
	if err := c.Parse(interleave(c.FlagSet, args)); err != nil {
		return false
	}
	if n := c.NArg(); n < min || (max >= 0 && n > max) {
		c.Usage()
		return false
	}
	return true
}

// writeJSON prints v as indented JSON, as every --json output is.
func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
