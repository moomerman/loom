package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// loom pprof saves one of a running app's Go profiles to a file, for
// `go tool pprof`. It exists because go tool pprof can't send the
// inspect token a deployed app needs: this command fetches the profile
// with it, and pprof reads the file.

// profiles are the profiles `loom pprof` fetches, each with the query
// it asks for. The heap profile collects garbage first (gc=1), so it
// shows what is live now rather than as of the last collection.
var profiles = map[string]string{
	"heap":      "?gc=1",
	"allocs":    "",
	"goroutine": "",
}

// PprofResult is what `loom pprof --json` prints: where the profile went.
type PprofResult struct {
	Profile string    `json:"profile"` // heap, allocs or goroutine
	File    string    `json:"file"`
	Bytes   int       `json:"bytes"`
	Taken   time.Time `json:"taken"`
}

func runPprof(args []string, stdout, stderr io.Writer) int {
	var out string
	return introspectFlags("pprof", "pprof [--json] [--url URL] [--out FILE] heap|allocs|goroutine", args, stdout, stderr, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&out, "out", "", "the file to write (default PROFILE-YYYYMMDD-HHMMSS.pb.gz)")
	}, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		name := args[0]
		query, ok := profiles[name]
		if !ok {
			return nil, nil, fmt.Errorf("%w: unknown profile %q: use heap, allocs or goroutine (a CPU profile or trace runs only under loom dev: go tool pprof %s/_loom/dev/pprof/profile)", errNoApp, name, c.base)
		}
		taken := time.Now()
		body, err := c.doTimeout(time.Minute, "GET", "/_loom/dev/pprof/"+name+query, nil)
		if err != nil {
			return body, nil, err
		}
		if out == "" {
			out = fmt.Sprintf("%s-%s.pb.gz", name, taken.Format("20060102-150405"))
		}
		if err := os.WriteFile(out, body, 0o644); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", errNoApp, err)
		}
		res := PprofResult{Profile: name, File: out, Bytes: len(body), Taken: taken.UTC()}
		data, _ := json.MarshalIndent(res, "", "  ")
		return append(data, '\n'), func(w io.Writer) error {
			fmt.Fprintf(w, "wrote the %s profile to %s (%s)\n", name, out, bytesText(int64(len(body))))
			switch name {
			case "heap":
				fmt.Fprintf(w, "  live memory by function:     go tool pprof -top -sample_index=inuse_space %s\n", out)
				fmt.Fprintf(w, "  growth since an earlier one: go tool pprof -top -sample_index=inuse_space -diff_base=EARLIER.pb.gz %s\n", out)
				fmt.Fprintf(w, "  in the browser:              go tool pprof -http=:8080 %s\n", out)
			case "allocs":
				fmt.Fprintf(w, "  allocated since the start:   go tool pprof -top -sample_index=alloc_space %s\n", out)
			case "goroutine":
				fmt.Fprintf(w, "  goroutines by stack:         go tool pprof -top %s\n", out)
				fmt.Fprintf(w, "  growth since an earlier one: go tool pprof -top -diff_base=EARLIER.pb.gz %s\n", out)
			}
			return nil
		}, nil
	})
}
