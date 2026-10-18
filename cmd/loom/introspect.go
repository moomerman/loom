package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/moomerman/loom"
)

// The introspection commands ask a running app, in dev mode, about itself
// through its /_loom/dev/ endpoints. Each prints for people, or with
// --json the endpoint's JSON as is (see docs/cli.md).

const defaultURL = "http://localhost:4000"

// appURL is the default --url: $LOOM_URL, else the app loom dev serves
// on $PORT, else defaultURL.
func appURL() string {
	if v := os.Getenv("LOOM_URL"); v != "" {
		return v
	}
	if port := os.Getenv("PORT"); port != "" {
		return "http://localhost:" + port
	}
	return defaultURL
}

// devAddr is the default address loom dev serves on: $LOOM_DEV_ADDR,
// else localhost:$PORT, else localhost:4000.
func devAddr() string {
	if v := os.Getenv("LOOM_DEV_ADDR"); v != "" {
		return v
	}
	if port := os.Getenv("PORT"); port != "" {
		return "localhost:" + port
	}
	return "localhost:4000"
}

// devClient talks to an app's /_loom/dev/ endpoints.
type devClient struct {
	base  string
	http  *http.Client
	db    *dbOpts // the database, for loom mcp's database tools
	token string  // the app's InspectToken, for a deployed app (--token, $LOOM_INSPECT_TOKEN)
}

// authorize adds the inspect token to req, if there is one.
func (c *devClient) authorize(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// apiError is an error answered by the app, in the form of docs/errors.md.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

func (e *apiError) Error() string {
	s := fmt.Sprintf("error %s: %s", e.Code, e.Message)
	if e.Fix != "" {
		s += "\n  fix: " + e.Fix
	}
	return s
}

// errNoApp means nothing usable answered at the URL: a usage-level error.
var errNoApp = errors.New("no app")

// doTimeout is do with a longer timeout, for a request that waits.
func (c *devClient) doTimeout(d time.Duration, method, path string, body []byte) ([]byte, error) {
	cc := *c
	h := *c.http
	h.Timeout = d
	cc.http = &h
	return cc.do(method, path, body)
}

// do sends a request and returns the response body. Errors the app
// answers are *apiError; connection problems wrap errNoApp.
func (c *devClient) do(method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoApp, err)
	}
	c.authorize(req)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: nothing answered at %s (%v); start the app with `loom dev`, or pass --url", errNoApp, c.base, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoApp, err)
	}
	if res.StatusCode == http.StatusOK {
		return data, nil
	}
	var e apiError
	if json.Unmarshal(data, &e) == nil && e.Code != "" {
		return data, &e
	}
	if res.StatusCode == http.StatusNotFound {
		if c.token != "" {
			return nil, fmt.Errorf("%w: the app at %s didn't accept the inspect token: set the same LOOM_INSPECT_TOKEN (or Config.InspectToken) on the app, or run it with `loom dev`", errNoApp, c.base)
		}
		return nil, fmt.Errorf("%w: the app at %s isn't in dev mode; run it with `loom dev`, or set App.Dev; for a deployed app, pass its inspect token with --token or $LOOM_INSPECT_TOKEN", errNoApp, c.base)
	}
	return nil, fmt.Errorf("%w: %s %s: %s: %s", errNoApp, method, path, res.Status, strings.TrimSpace(string(data)))
}

// introspect parses the common flags and runs a command against the app.
// run gets the client and the remaining arguments; it prints the human
// form, or the raw JSON when --json is set.
func introspect(name, usageLine string, args []string, stdout, stderr io.Writer, nargs int,
	run func(c *devClient, args []string) (data []byte, human func(w io.Writer) error, err error)) int {
	return introspectFlags(name, usageLine, args, stdout, stderr, nargs, nil, run)
}

// introspectFlags is introspect with extra flags, defined by setup.
func introspectFlags(name, usageLine string, args []string, stdout, stderr io.Writer, nargs int, setup func(fs *flag.FlagSet),
	run func(c *devClient, args []string) (data []byte, human func(w io.Writer) error, err error)) int {
	fs := newCommand(name, usageLine, "print the app's JSON", stderr)
	asJSON := fs.JSON
	url := fs.String("url", appURL(), "the app's URL (default $LOOM_URL, or http://localhost:$PORT, or "+defaultURL+")")
	token := fs.String("token", os.Getenv("LOOM_INSPECT_TOKEN"), "a deployed app's inspect token (default $LOOM_INSPECT_TOKEN)")
	assigns := ""
	if name == "render" {
		fs.StringVar(&assigns, "assigns", "", "a JSON file of assigns, or - for stdin (default {})")
	}
	if setup != nil {
		setup(fs.FlagSet)
	}
	if !fs.parse(args, max(nargs, 0), nargs) { // nargs -1: any number
		return 2
	}
	rest := fs.Args()
	if name == "render" {
		rest = append(rest, assigns)
	}
	c := &devClient{base: strings.TrimRight(*url, "/"), http: &http.Client{Timeout: 10 * time.Second}, token: *token}
	data, human, err := run(c, rest)
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		if *asJSON {
			stdout.Write(data)
		} else {
			fmt.Fprintln(stdout, ae.Error())
		}
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "loom %s: %v\n", name, strings.TrimPrefix(err.Error(), errNoApp.Error()+": "))
		return 2
	}
	if *asJSON {
		stdout.Write(data)
		return 0
	}
	if err := human(stdout); err != nil {
		fmt.Fprintf(stderr, "loom %s: %v\n", name, err)
		return 2
	}
	return 0
}

// interleave moves flags before positional arguments, so both
// `loom inspect --json SID` and `loom inspect SID --json` work. A flag
// that takes a value (any non-bool flag in fs) keeps the argument after it.
func interleave(fs *flag.FlagSet, args []string) []string {
	takesValue := func(a string) bool {
		name := strings.TrimLeft(a, "-")
		f := fs.Lookup(name)
		if f == nil {
			return false
		}
		b, ok := f.Value.(interface{ IsBoolFlag() bool })
		return !ok || !b.IsBoolFlag()
	}
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos = append(pos, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-") && a != "-":
			flags = append(flags, a)
			if !strings.Contains(a, "=") && takesValue(a) && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		default:
			pos = append(pos, a)
		}
	}
	return append(flags, pos...)
}

func runRoutes(args []string, stdout, stderr io.Writer) int {
	return introspect("routes", "routes [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/routes", nil)
		return data, func(w io.Writer) error {
			var routes []loom.RouteInfo
			if err := json.Unmarshal(data, &routes); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "PATH\tPAGE\tCOMPONENT\tLAYOUT\tROOT LAYOUT\tMOUNT\tAUTH\tWATCH\tRATE LIMIT")
			dash := func(s string) string {
				if s == "" {
					return "-"
				}
				return s
			}
			for _, r := range routes {
				mount := ""
				if r.Mount != "" {
					mount = r.Mount + " at " + r.Prefix
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Path, r.Page, r.Component, dash(r.Layout), r.RootLayout, dash(mount), dash(strings.Join(r.Auth, ", ")), dash(strings.Join(r.Watch, ", ")), dash(strings.Join(r.RateLimit, "; ")))
			}
			return tw.Flush()
		}, err
	})
}

func runManifest(args []string, stdout, stderr io.Writer) int {
	return introspect("manifest", "manifest [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/manifest", nil)
		return data, func(w io.Writer) error {
			var m loom.Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				return err
			}
			_, err := io.WriteString(w, m.Text())
			return err
		}, err
	})
}

func runSessions(args []string, stdout, stderr io.Writer) int {
	return introspect("sessions", "sessions [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/sessions", nil)
		return data, func(w io.Writer) error {
			var list []loom.SessionInfo
			if err := json.Unmarshal(data, &list); err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Fprintln(w, "no sessions: open a page in a browser")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "SID\tURL\tVERSION\tSTATE\tRESTARTS")
			for _, s := range list {
				state := fmt.Sprintf("connected (%d)", s.Connected)
				if s.Connected == 0 {
					state = fmt.Sprintf("idle %ds", int(s.IdleSeconds))
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\n", s.SID, s.URL, s.Version, state, s.Restarts)
			}
			return tw.Flush()
		}, err
	})
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	return introspect("inspect", "inspect [--json] [--url URL] SID", args, stdout, stderr, 1, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/sessions/"+args[0], nil)
		return data, func(w io.Writer) error {
			var d loom.SessionDetail
			if err := json.Unmarshal(data, &d); err != nil {
				return err
			}
			assigns, _ := json.MarshalIndent(d.Assigns, "", "  ")
			fmt.Fprintf(w, "session %s: %s (%s, %s), version %d\n\nassigns:\n%s\n", d.SID, d.Path, d.Page, d.Component, d.Version, assigns)
			if d.LayoutAssigns != nil {
				la, _ := json.MarshalIndent(d.LayoutAssigns, "", "  ")
				fmt.Fprintf(w, "\nlayout assigns:\n%s\n", la)
			}
			if len(d.Timers) > 0 {
				fmt.Fprintln(w, "\ntimers:")
				for _, t := range d.Timers {
					fmt.Fprintf(w, "  %s in %dms %v\n", t.Event, t.InMS, t.Payload)
				}
			}
			fmt.Fprintf(w, "\nhtml:\n%s\n", d.HTML)
			return nil
		}, err
	})
}

func runRender(args []string, stdout, stderr io.Writer) int {
	return introspect("render", "render [--json] [--url URL] [--assigns FILE|-] COMPONENT", args, stdout, stderr, 1, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		var body []byte
		var err error
		switch file := args[1]; file {
		case "":
			body = []byte("{}")
		case "-":
			body, err = io.ReadAll(os.Stdin)
		default:
			body, err = os.ReadFile(file)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: reading assigns: %v", errNoApp, err)
		}
		data, err := c.do("POST", "/_loom/dev/render?component="+args[0], body)
		return data, func(w io.Writer) error {
			var r loom.RenderResult
			if err := json.Unmarshal(data, &r); err != nil {
				return err
			}
			fmt.Fprintln(w, r.HTML)
			return nil
		}, err
	})
}

func runErrors(args []string, stdout, stderr io.Writer) int {
	return introspect("errors", "errors [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/errors", nil)
		return data, func(w io.Writer) error {
			var list []loom.ErrorInfo
			if err := json.Unmarshal(data, &list); err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Fprintln(w, "no runtime errors")
			}
			for _, e := range list {
				printError(w, e)
			}
			return nil
		}, err
	})
}

func runMail(args []string, stdout, stderr io.Writer) int {
	return introspect("mail", "mail [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/mail", nil)
		return data, func(w io.Writer) error {
			var list []loom.SentMail
			if err := json.Unmarshal(data, &list); err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Fprintln(w, "no mail sent (the app keeps it here in dev when it has no mailer)")
			}
			for i, m := range list {
				if i > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "%s to %s: %s\n", m.Time.Local().Format("15:04:05"), m.To, m.Subject)
				for _, line := range strings.Split(strings.TrimRight(m.Text, "\n"), "\n") {
					fmt.Fprintln(w, "  "+line)
				}
			}
			return nil
		}, err
	})
}

func runStorage(args []string, stdout, stderr io.Writer) int {
	var after string
	var limit int
	return introspectFlags("storage", "storage [--json] [--url URL] [--after KEY] [--limit N] [PREFIX]", args, stdout, stderr, -1, func(fs *flag.FlagSet) {
		fs.StringVar(&after, "after", "", "list keys after this one, for the next page")
		fs.IntVar(&limit, "limit", 100, "at most this many objects")
	}, func(c *devClient, rest []string) ([]byte, func(io.Writer) error, error) {
		q := url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}
		if len(rest) > 0 {
			q.Set("prefix", rest[0])
		}
		data, err := c.do("GET", "/_loom/dev/storage?"+q.Encode(), nil)
		return data, func(w io.Writer) error {
			var list loom.StorageList
			if err := json.Unmarshal(data, &list); err != nil {
				return err
			}
			fmt.Fprintln(w, "bucket: "+list.Bucket)
			if len(list.Objects) == 0 {
				fmt.Fprintln(w, "no objects")
			}
			for _, o := range list.Objects {
				fmt.Fprintf(w, "%10s  %s  %s\n", humanBytes(o.Size), o.Modified.Local().Format("2006-01-02 15:04"), o.Key)
			}
			if len(list.Objects) == limit {
				fmt.Fprintf(w, "more: loom storage --after %q", list.Objects[len(list.Objects)-1].Key)
				if len(rest) > 0 {
					fmt.Fprintf(w, " %q", rest[0])
				}
				fmt.Fprintln(w)
			}
			return nil
		}, err
	})
}

// humanBytes is n as "512 B", "1.2 KB", "3.4 MB".
func humanBytes(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f KB", float64(n)/1000)
	case n < 1000*1000*1000:
		return fmt.Sprintf("%.1f MB", float64(n)/1000/1000)
	}
	return fmt.Sprintf("%.1f GB", float64(n)/1000/1000/1000)
}

// printError prints a runtime error for people.
func printError(w io.Writer, e loom.ErrorInfo) {
	from := "session " + e.SID
	if e.SID == "" {
		from = e.Source
	} else if e.Source == "browser" {
		from = "browser, " + from
	}
	fmt.Fprintf(w, "%s %s: error %s: %s\n", e.Time.Local().Format("15:04:05"), from, e.Code, e.Message)
	if e.URL != "" {
		fmt.Fprintf(w, "  on %s\n", e.URL)
	}
	if e.Where != "" {
		fmt.Fprintf(w, "  at %s\n", e.Where)
	}
	if e.Fix != "" {
		fmt.Fprintf(w, "  fix: %s\n", e.Fix)
	}
}

// runEvent sends an event to a session, as its browser would, and prints
// the errors it caused and the page afterwards.
func runEvent(args []string, stdout, stderr io.Writer) int {
	return introspect("event", "event [--json] [--url URL] SID EVENT [KEY=VALUE ...]", args, stdout, stderr, -1, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		if len(args) < 2 {
			return nil, nil, fmt.Errorf("%w: give a session id (from `loom sessions`) and an event name, then any payload as KEY=VALUE", errNoApp)
		}
		body, err := eventBody(args[1], args[2:])
		if err != nil {
			return nil, nil, err
		}
		data, err := c.do("POST", "/_loom/dev/sessions/"+url.PathEscape(args[0])+"/event", body)
		return data, func(w io.Writer) error {
			var r loom.EventResult
			if err := json.Unmarshal(data, &r); err != nil {
				return err
			}
			for _, e := range r.Errors {
				printError(w, e)
			}
			if r.Ended {
				fmt.Fprintf(w, "the session ended; its browser reloads\n")
				return nil
			}
			assigns, _ := json.MarshalIndent(r.Assigns, "", "  ")
			fmt.Fprintf(w, "session %s: %s, version %d\n\nassigns:\n%s\n\nhtml:\n%s\n", r.SID, r.Path, r.Version, assigns, r.HTML)
			return nil
		}, err
	})
}

// eventBody is the JSON of an event request, from KEY=VALUE pairs.
func eventBody(event string, pairs []string) ([]byte, error) {
	payload := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%w: payload %q isn't KEY=VALUE", errNoApp, p)
		}
		payload[k] = v
	}
	return json.Marshal(map[string]any{"event": event, "payload": payload})
}
