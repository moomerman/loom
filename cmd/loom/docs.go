package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/moomerman/loom/docs"
)

// `loom docs` prints Loom's documentation, embedded in the binary, so an
// agent working on an app outside this repository can read it.

// docsList is the JSON form of `loom docs --json`.
type docsList struct {
	Topics []docs.Topic `json:"topics"`
}

// docsPage is the JSON form of `loom docs --json TOPIC`.
type docsPage struct {
	docs.Topic
	Content string `json:"content"`
}

// docsJSON returns the JSON of `loom docs --json [TOPIC]`, for the CLI
// and the docs MCP tool.
func docsJSON(topic string) (any, error) {
	if topic == "" {
		return docsList{Topics: docs.Topics()}, nil
	}
	t, md, err := docs.Lookup(topic)
	if err != nil {
		return nil, err
	}
	return docsPage{Topic: t, Content: md}, nil
}

func runDocs(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("docs", "docs [--json] [TOPIC]", "print the result as JSON", stderr)
	if !fs.parse(args, 0, 1) {
		return 2
	}
	asJSON := fs.JSON
	out, err := docsJSON(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "loom docs: %v\n", err)
		return 2
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		enc.Encode(out)
		return 0
	}
	switch out := out.(type) {
	case docsPage:
		fmt.Fprint(stdout, out.Content)
	case docsList:
		fmt.Fprintln(stdout, "Loom's docs, in reading order. Run `loom docs TOPIC` to read one.")
		fmt.Fprintln(stdout)
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		for _, t := range out.Topics {
			fmt.Fprintf(w, "  %s\t%s\n", t.Name, t.Summary)
		}
		w.Flush()
	}
	return 0
}
