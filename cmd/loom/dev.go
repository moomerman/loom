package main

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/data"
)

// `loom dev` runs an app for development. It watches the .hx and .go
// files, rebuilds templates and the app when they change, restarts the
// app and reloads the browsers. It serves the app through a proxy, which
// keeps the address stable across restarts and shows build errors as an
// overlay in the browser.

//go:embed devclient.js
var devClientJS []byte

const (
	devReloadPath = "/_loom/dev/reload"    // SSE: build state for the overlay
	devClientPath = "/_loom/dev/client.js" // the overlay script
	devStatusPath = "/_loom/dev/status"    // JSON: the build state, for `loom status`
	pollInterval  = 100 * time.Millisecond
	settleTime    = 50 * time.Millisecond
)

// devEvent is one line of `loom dev --json` output, and the data of an
// overlay event. See docs/cli.md.
type devEvent struct {
	Event       string            `json:"event"` // "serving", "building", "migrated", "ready", "error", "exit"
	URL         string            `json:"url,omitempty"`
	Package     string            `json:"package,omitempty"` // for "serving": the main package it runs
	Changed     []string          `json:"changed,omitempty"`
	Ms          int64             `json:"ms,omitempty"`
	Kind        string            `json:"kind,omitempty"`    // for "error": "migration", "template", "go" or "exit"
	Applied     []string          `json:"applied,omitempty"` // for "migrated": the migration files applied
	Diagnostics []diag.Diagnostic `json:"diagnostics,omitempty"`
	Output      string            `json:"output,omitempty"` // formatted errors or process output
}

type devServer struct {
	pkg        string   // the main package to build
	watch      []string // directories to watch
	addr       string   // where the proxy listens
	asJSON     bool
	dbURL      string    // apply migrations to this database, if set
	migrations string    // the migrations directory
	out        io.Writer // events
	appOut     io.Writer // the app's own output
	binPath    string

	mu       sync.Mutex
	state    *devEvent // the last "ready" or "error" event
	building bool      // a rebuild is running
	builds   int       // rebuilds finished
	clients  map[chan devEvent]bool
	app      *exec.Cmd
	appAddr  string
	appDone  chan struct{} // closed when app exits
	token    string        // LOOM_DEV_TOKEN for this run; the proxy adds it to every request
}

func runDev(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("dev", "dev [--json] [--addr localhost:4000] [--watch DIR]... [--db URL] [--migrations DIR] [PACKAGE]", "print events as JSON lines", stderr)
	asJSON := fs.JSON
	addr := fs.String("addr", devAddr(), "address to serve on (default $LOOM_DEV_ADDR, or localhost:$PORT, or localhost:4000)")
	var watch multiFlag
	fs.Var(&watch, "watch", "directory to watch, repeatable (default .)")
	dbURL := fs.String("db", os.Getenv("DATABASE_URL"), "apply migrations to this database on start and on change (default $DATABASE_URL)")
	migrations := fs.String("migrations", defaultMigrations, "the migrations directory")
	if !fs.parse(args, 0, 1) {
		return 2
	}
	var pkg string
	if fs.NArg() == 1 {
		pkg = fs.Arg(0)
	} else {
		var err error
		if pkg, err = findDevMain("."); err != nil {
			fmt.Fprintf(stderr, "loom dev: %v\n", err)
			return 2
		}
	}
	if len(watch) == 0 {
		watch = []string{"."}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	d := &devServer{pkg: pkg, watch: watch, addr: *addr, asJSON: *asJSON, dbURL: *dbURL, migrations: *migrations, out: stdout, appOut: stderr}
	if err := d.run(ctx); err != nil {
		fmt.Fprintf(stderr, "loom dev: %v\n", err)
		return 2
	}
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// run serves until ctx is done.
func (d *devServer) run(ctx context.Context) error {
	tmp, err := os.MkdirTemp("", "loom-dev-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	d.binPath = filepath.Join(tmp, "app")
	d.clients = map[chan devEvent]bool{}

	ln, err := net.Listen("tcp", d.addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: d}
	go srv.Serve(ln)
	defer srv.Close()
	d.emit(devEvent{Event: "serving", URL: "http://" + displayAddr(ln.Addr().String()), Package: d.pkg})

	defer d.stopApp()
	files := snapshot(d.watch, d.migrations)
	d.rebuild(nil)
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		now := snapshot(d.watch, d.migrations)
		changed := changes(files, now)
		if len(changed) == 0 {
			continue
		}
		// Wait for a burst of saves (an editor or git writing several
		// files) to finish, then build once.
		for {
			time.Sleep(settleTime)
			later := snapshot(d.watch, d.migrations)
			if len(changes(now, later)) == 0 {
				break
			}
			now = later
		}
		d.rebuild(changes(files, now))
		// Compare later scans with what was built, so edits made during
		// the build trigger another. Generated *_hx files aren't
		// watched, so the build's own writes don't.
		files = now
	}
}

func displayAddr(a string) string {
	host, port, err := net.SplitHostPort(a)
	if err == nil && (host == "127.0.0.1" || host == "::1" || host == "::" || host == "0.0.0.0") {
		return "localhost:" + port
	}
	return a
}

// rebuild applies pending migrations, builds templates and the app, then
// restarts it. On failure the old app keeps running and browsers show
// the errors.
func (d *devServer) rebuild(changed []string) {
	start := time.Now()
	d.mu.Lock()
	d.building = true
	d.mu.Unlock()
	d.emit(devEvent{Event: "building", Changed: changed})

	if err := d.migrate(); err != nil {
		d.fail(devEvent{Kind: "migration", Output: err.Error()})
		return
	}
	res, _, err := compiler.Build(d.watch...)
	if err != nil {
		d.fail(devEvent{Kind: "template", Output: err.Error()})
		return
	}
	if res.Diagnostics.HasErrors() {
		var text strings.Builder
		for _, e := range res.Diagnostics {
			text.WriteString(e.Error() + "\n")
		}
		d.fail(devEvent{Kind: "template", Diagnostics: diagnostics(res.Diagnostics), Output: text.String()})
		return
	}
	cmd := exec.Command("go", "build", "-o", d.binPath, d.pkg)
	if out, err := cmd.CombinedOutput(); err != nil {
		d.fail(devEvent{Kind: "go", Output: string(out) + err.Error()})
		return
	}
	if err := d.restartApp(); err != nil {
		d.fail(devEvent{Kind: "exit", Output: err.Error()})
		return
	}
	d.setState(devEvent{Event: "ready", Ms: time.Since(start).Milliseconds()})
}

// migrate applies pending migrations when loom dev has a database and
// the migrations directory exists. Production apps are migrated only by
// `loom migrate`.
func (d *devServer) migrate() error {
	if d.dbURL == "" {
		return nil
	}
	if _, err := os.Stat(d.migrations); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := data.Open(ctx, d.dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	applied, err := data.Migrate(ctx, db, os.DirFS(d.migrations))
	var files []string
	for _, a := range applied {
		files = append(files, filepath.Join(d.migrations, a.File))
	}
	if len(files) > 0 {
		d.emit(devEvent{Event: "migrated", Applied: files})
	}
	return err
}

func (d *devServer) fail(e devEvent) {
	e.Event = "error"
	d.setState(e)
}

// setState records and broadcasts a ready or error event. Browsers
// reload on ready and show an overlay on error.
func (d *devServer) setState(e devEvent) {
	d.mu.Lock()
	d.state = &e
	if d.building {
		d.building = false
		d.builds++
	}
	for ch := range d.clients {
		select {
		case ch <- e:
		default:
		}
	}
	d.mu.Unlock()
	d.emit(e)
}

// emit prints an event for the person or agent running loom dev.
func (d *devServer) emit(e devEvent) {
	if d.asJSON {
		b, _ := json.Marshal(e)
		fmt.Fprintf(d.out, "%s\n", b)
		return
	}
	switch e.Event {
	case "serving":
		fmt.Fprintf(d.out, "loom dev: serving %s (%s)\n", e.URL, e.Package)
	case "building":
		if len(e.Changed) > 0 {
			fmt.Fprintf(d.out, "loom dev: %s changed, rebuilding\n", strings.Join(e.Changed, ", "))
		}
	case "migrated":
		fmt.Fprintf(d.out, "loom dev: applied %s\n", strings.Join(e.Applied, ", "))
	case "ready":
		fmt.Fprintf(d.out, "loom dev: ready in %dms\n", e.Ms)
	case "error", "exit":
		fmt.Fprintf(d.out, "loom dev: %s error\n%s\n", e.Kind, strings.TrimRight(e.Output, "\n"))
	}
}

// devToken returns this run's LOOM_DEV_TOKEN, made on first use. The
// app answers dev requests only with it, and only the proxy adds it,
// so the app's dev endpoints are reachable through loom dev alone.
func (d *devServer) devToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		d.token = hex.EncodeToString(b)
	}
	return d.token
}

// restartApp stops the running app, starts the new build and waits until
// it accepts connections.
func (d *devServer) restartApp() error {
	d.stopApp()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	appAddr := ln.Addr().String()
	ln.Close()

	var output bytes.Buffer
	cmd := exec.Command(d.binPath)
	cmd.Env = append(os.Environ(), "LOOM_DEV=1", "LOOM_DEV_TOKEN="+d.devToken(), "LOOM_ADDR="+appAddr)
	cmd.Stdout = io.MultiWriter(d.appOut, &output)
	cmd.Stderr = io.MultiWriter(d.appOut, &output)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
		d.mu.Lock()
		current := d.app == cmd
		d.mu.Unlock()
		if current {
			// It stopped by itself: show why.
			d.fail(devEvent{Kind: "exit", Output: fmt.Sprintf("the app exited (%v):\n%s", cmd.ProcessState, tail(output.String(), 4000))})
		}
	}()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case <-done:
			return fmt.Errorf("the app exited during startup (%v):\n%s\nDoes main call app.Run? Under loom dev it must serve with App.Run, which listens on $LOOM_ADDR", cmd.ProcessState, tail(output.String(), 4000))
		default:
		}
		if c, err := net.DialTimeout("tcp", appAddr, 100*time.Millisecond); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			return fmt.Errorf("the app didn't listen on %s ($LOOM_ADDR) within 15s; serve it with App.Run", appAddr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.mu.Lock()
	d.app, d.appAddr, d.appDone = cmd, appAddr, done
	d.mu.Unlock()
	return nil
}

func (d *devServer) stopApp() {
	d.mu.Lock()
	cmd, done := d.app, d.appDone
	d.app = nil
	d.mu.Unlock()
	if cmd == nil {
		return
	}
	cmd.Process.Signal(os.Interrupt)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// ServeHTTP serves the overlay endpoints and proxies everything else to
// the app.
func (d *devServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case devReloadPath:
		d.serveReload(w, r)
		return
	case devClientPath:
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(devClientJS)
		return
	case devStatusPath:
		d.serveStatus(w, r)
		return
	}
	d.mu.Lock()
	appAddr := d.appAddr
	running := d.app != nil
	d.mu.Unlock()
	if !running {
		d.serveNoApp(w, r)
		return
	}
	target := &url.URL{Scheme: "http", Host: appAddr}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set(loom.DevTokenHeader, d.devToken())
		},
		FlushInterval:  -1, // stream SSE immediately
		ModifyResponse: injectClient,
		ErrorHandler:   func(w http.ResponseWriter, r *http.Request, err error) { d.serveNoApp(w, r) },
	}
	proxy.ServeHTTP(w, r)
}

// injectClient adds the overlay script to HTML pages.
func injectClient(res *http.Response) error {
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || res.Header.Get("Content-Encoding") != "" {
		return nil
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return err
	}
	tag := []byte(`<script src="` + devClientPath + `"></script>`)
	if i := bytes.LastIndex(body, []byte("</body>")); i >= 0 {
		body = append(body[:i:i], append(tag, body[i:]...)...)
	} else {
		body = append(body, tag...)
	}
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	res.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

// serveNoApp answers while no app is running: pages get an empty page
// with the overlay, which reloads when a build succeeds.
func (d *devServer) serveNoApp(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"><title>loom dev</title></head><body><p>Waiting for the app to build.</p><script src=%q></script></body></html>\n", devClientPath)
		return
	}
	http.Error(w, "loom dev: the app isn't running; see the loom dev output", http.StatusBadGateway)
}

// devStatus is the JSON of `loom status --json`: loom dev's build state.
type devStatus struct {
	State   string    `json:"state"`   // "building", "ready" or "error"
	Running bool      `json:"running"` // the app is running; after an error, the last good build is
	Builds  int       `json:"builds"`  // rebuilds finished since loom dev started
	Last    *devEvent `json:"last"`    // the last "ready" or "error" event; null before the first build ends
}

func (d *devServer) status() devStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := devStatus{State: "building", Running: d.app != nil, Builds: d.builds, Last: d.state}
	if !d.building && d.state != nil {
		st.State = d.state.Event
	}
	return st
}

// serveStatus answers the build state. With ?wait=true it first waits
// for the build of a change saved just before to finish (giving the file
// watcher a moment to notice it), so an agent can save, then ask once.
func (d *devServer) serveStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("wait") == "true" {
		start := d.status().Builds
		notice := time.Now().Add(4*pollInterval + 2*settleTime)
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) && r.Context().Err() == nil {
			st := d.status()
			if st.State != "building" && (st.Builds > start || time.Now().After(notice)) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(d.status())
}

// serveReload streams build state to the overlay script.
func (d *devServer) serveReload(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := make(chan devEvent, 8)
	d.mu.Lock()
	d.clients[ch] = true
	state := d.state
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.clients, ch)
		d.mu.Unlock()
	}()
	if state != nil && state.Event == "error" {
		writeDevEvent(w, *state)
	}
	flusher.Flush()
	for {
		select {
		case e := <-ch:
			writeDevEvent(w, e)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeDevEvent(w io.Writer, e devEvent) {
	name := e.Event
	if name == "error" {
		name = "failed" // EventSource uses "error" for connection errors
	}
	b, _ := json.Marshal(e)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
}

// fileState identifies a version of a file.
type fileState struct {
	mod  time.Time
	size int64
}

// snapshot records the .go and .hx files under the watched directories,
// skipping generated *_hx.go and *_hx.js files and the directories the go tool skips,
// and the .sql files in the migrations directory.
func snapshot(dirs []string, migrations string) map[string]fileState {
	files := map[string]fileState{}
	if entries, err := os.ReadDir(migrations); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".sql") {
				files[filepath.Join(migrations, e.Name())] = fileState{info.ModTime(), info.Size()}
			}
		}
	}
	for _, root := range dirs {
		filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := e.Name()
			if e.IsDir() {
				if path != root && (name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			// Templates, Go, tokens, and behaviors' modules and helpers.js
			// (embedded); not the generated _hx.go and _hx.js.
			generated := strings.HasSuffix(name, "_hx.go") || strings.HasSuffix(name, "_hx.js")
			watched := !generated && (strings.HasSuffix(name, ".hx") || strings.HasSuffix(name, ".go") || name == "tokens.toml" || strings.HasSuffix(name, ".js"))
			if watched {
				if info, err := e.Info(); err == nil {
					files[path] = fileState{info.ModTime(), info.Size()}
				}
			}
			return nil
		})
	}
	return files
}

// changes lists the files added, changed or removed between snapshots.
func changes(old, now map[string]fileState) []string {
	var out []string
	for p, s := range now {
		if o, ok := old[p]; !ok || o != s {
			out = append(out, p)
		}
	}
	for p := range old {
		if _, ok := now[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
