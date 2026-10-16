package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// testApp writes a small Loom app module to a temp dir.
func testApp(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return writeFiles(t, map[string]string{
		"go.mod": appGoMod(t, root),
		"go.sum": loomGoSum(t, root),
		"main.go": `package main

import (
	"log"

	"example.com/app/templates"
	"github.com/moomerman/loom"
)

type page struct{ n int }

func (p *page) Mount(*loom.Ctx) {}

func (p *page) Events() loom.Events {
	return loom.Events{"inc": func(*loom.Ctx, loom.Payload) { p.n++ }}
}

func (p *page) Assigns(*loom.Ctx) templates.HomeAssigns { return templates.HomeAssigns{N: p.n} }

func main() {
	app := loom.New()
	app.Live("/", &page{}, templates.Home)
	if err := app.Run(":0"); err != nil {
		log.Fatal(err)
	}
}
`,
		"templates/doc.go":  "package templates\n",
		"templates/home.hx": "component Home(n int)\n<p id=\"msg\">Hello {n}</p>\n",
	})
}

// startDev runs loom dev on dir and returns its URL and its JSON events.
func startDev(t *testing.T, dir string) (string, <-chan devEvent, *devServer) {
	t.Helper()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	pr, pw := io.Pipe()
	d := &devServer{pkg: ".", watch: []string{"."}, addr: addr, asJSON: true, out: pw, appOut: io.Discard}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; pw.Close() })

	events := make(chan devEvent, 32)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var e devEvent
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				t.Errorf("bad JSON line %q: %v", sc.Text(), err)
			}
			events <- e
		}
	}()
	return "http://" + addr, events, d
}

// waitEvent returns the next ready or error event.
func waitEvent(t *testing.T, events <-chan devEvent) devEvent {
	t.Helper()
	timeout := time.After(60 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Event == "ready" || e.Event == "error" {
				return e
			}
		case <-timeout:
			t.Fatal("timed out waiting for loom dev")
		}
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	// Make sure the modification time changes even on coarse clocks.
	time.Sleep(20 * time.Millisecond)
	// Write atomically, as editors do, so the watcher never sees a
	// half-written file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func TestDev(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an app")
	}
	dir := testApp(t)
	url, events, d := startDev(t, dir)
	if e := waitEvent(t, events); e.Event != "ready" {
		t.Fatalf("first build: %+v", e)
	}
	page := get(t, url+"/")
	if !strings.Contains(page, `<p id="msg">Hello 0</p>`) || !strings.Contains(page, devClientPath) {
		t.Fatalf("page:\n%s", page)
	}

	// Introspection works through the proxy, against the app in dev mode.
	var out, errOut strings.Builder
	if code := runRoutes([]string{"--url", url}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "templates.Home") {
		t.Errorf("loom routes: %d %s%s", code, out.String(), errOut.String())
	}
	// Only through it: the app's own address refuses dev requests without
	// the token the proxy adds.
	d.mu.Lock()
	appURL := "http://" + d.appAddr
	d.mu.Unlock()
	if res, err := http.Get(appURL + "/_loom/dev/routes"); err != nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("dev request straight to the app: %v %v, want 403", res, err)
	}

	// A template change rebuilds and restarts the app.
	start := time.Now()
	write(t, filepath.Join(dir, "templates/home.hx"), "component Home(n int)\n<p id=\"msg\">Bye {n}</p>\n")
	if e := waitEvent(t, events); e.Event != "ready" {
		t.Fatalf("after edit: %+v", e)
	}
	t.Logf("template change to ready: %v", time.Since(start))
	if page := get(t, url+"/"); !strings.Contains(page, "Bye 0") {
		t.Fatalf("after edit:\n%s", page)
	}

	// Overlay clients get errors as they happen.
	res, err := http.Get(url + devReloadPath)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	stream := bufio.NewReader(res.Body)

	// A template error is reported with its code; the old app keeps running.
	write(t, filepath.Join(dir, "templates/home.hx"), "component Home(n int)\n<p id=\"msg\">Bye {m}</p>\n")
	e := waitEvent(t, events)
	if e.Event != "error" || e.Kind != "template" || len(e.Diagnostics) != 1 || e.Diagnostics[0].Code != "TC301" {
		t.Fatalf("template error: %+v", e)
	}
	if page := get(t, url+"/"); !strings.Contains(page, "Bye 0") {
		t.Errorf("the previous build should keep serving:\n%s", page)
	}
	line, _ := stream.ReadString('\n')
	if line != "event: failed\n" {
		t.Errorf("overlay stream: %q", line)
	}
	// loom status says so too.
	out.Reset()
	if code := runStatus([]string{"--url", url, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("loom status: %d %s", code, errOut.String())
	}
	var st devStatus
	if err := json.Unmarshal([]byte(out.String()), &st); err != nil || st.State != "error" || !st.Running || st.Last.Kind != "template" || st.Last.Diagnostics[0].Code != "TC301" {
		t.Errorf("loom status: %s", out.String())
	}

	// A Go error is reported with the compiler's output.
	write(t, filepath.Join(dir, "templates/home.hx"), "component Home(n int)\n<p id=\"msg\">Bye {n}</p>\n")
	write(t, filepath.Join(dir, "main.go"), strings.Replace(readFile(t, filepath.Join(dir, "main.go")), "p.n++", "p.n++ +", 1))
	if e := waitEvent(t, events); e.Event != "error" || e.Kind != "go" || !strings.Contains(e.Output, "main.go:15") {
		t.Fatalf("go error: %+v", e)
	}
	write(t, filepath.Join(dir, "main.go"), strings.Replace(readFile(t, filepath.Join(dir, "main.go")), "p.n++ +", "p.n++", 1))
	if e := waitEvent(t, events); e.Event != "ready" {
		t.Fatalf("after fixing: %+v", e)
	}

	// loom status --wait waits for the rebuild of a change just saved.
	write(t, filepath.Join(dir, "templates/home.hx"), "component Home(n int)\n<p id=\"msg\">Hi {n}</p>\n")
	out.Reset()
	if code := runStatus([]string{"--url", url, "--wait"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "ready: built in ") {
		t.Errorf("loom status --wait: %d %q %s", code, out.String(), errOut.String())
	}
	if page := get(t, url+"/"); !strings.Contains(page, "Hi 0") {
		t.Errorf("after loom status --wait:\n%s", page)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// appGoMod returns the go.mod of an app module that uses the Loom at root,
// listing Loom's own requirements (pgx) as the go command expects.
func appGoMod(t *testing.T, root string) string {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	goLine := regexp.MustCompile(`(?m)^go \S+$`).Find(b)
	var reqs []string
	for _, m := range regexp.MustCompile(`(?m)^\t?(?:require )?([\w./-]+\.[\w./-]+ v\S+)`).FindAllStringSubmatch(string(b), -1) {
		reqs = append(reqs, "\t"+m[1]+" // indirect\n")
	}
	return "module example.com/app\n\n" + string(goLine) + "\n\nrequire github.com/moomerman/loom v0.0.0\n\nrequire (\n" + strings.Join(reqs, "") + ")\n\nreplace github.com/moomerman/loom => " + root + "\n"
}

// loomGoSum returns Loom's go.sum, which the app module needs for Loom's
// dependencies.
func loomGoSum(t *testing.T, root string) string {
	b, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
