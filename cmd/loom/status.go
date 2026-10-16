package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// runStatus implements `loom status`: ask loom dev whether the last
// rebuild worked, and why not.
func runStatus(args []string, stdout, stderr io.Writer) int {
	var wait bool
	setup := func(fs *flag.FlagSet) {
		fs.BoolVar(&wait, "wait", false, "first wait for the rebuild of a change just saved to finish")
	}
	return introspectFlags("status", "status [--json] [--url URL] [--wait]", args, stdout, stderr, 0, setup, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		path := devStatusPath
		if wait {
			path += "?wait=true"
			c.http.Timeout = 3 * time.Minute
		}
		data, err := c.do("GET", path, nil)
		return data, func(w io.Writer) error {
			var st devStatus
			if err := json.Unmarshal(data, &st); err != nil {
				return err
			}
			switch {
			case st.State == "building":
				fmt.Fprintln(w, "building")
			case st.State == "ready":
				fmt.Fprintf(w, "ready: built in %dms\n", st.Last.Ms)
			default:
				fmt.Fprintf(w, "%s error\n%s\n", st.Last.Kind, strings.TrimRight(st.Last.Output, "\n"))
				if st.Running {
					fmt.Fprintln(w, "the last good build is still running")
				}
			}
			return nil
		}, err
	})
}
