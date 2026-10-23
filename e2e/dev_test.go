package e2e

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// TestDevOverlay runs `loom dev` on a small app, breaks a template and
// checks the browser shows the error, then fixes it and checks the page
// reloads with the change.
func TestDevOverlay(t *testing.T) {
	ctx := browser(t)
	root, _ := filepath.Abs("..")
	bin := filepath.Join(t.TempDir(), "loom")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/moomerman/loom/cmd/loom").CombinedOutput(); err != nil {
		t.Fatalf("building loom: %v\n%s", err, out)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": appGoMod(t, root),
		"go.sum": loomGoSum(t, root),
		"main.go": `package main

import (
	"log"

	"example.com/app/templates"
	"github.com/moomerman/loom"
)

type page struct{}

func (p *page) Mount(*loom.Ctx) {}

func (p *page) Assigns(*loom.Ctx) templates.HomeAssigns { return templates.HomeAssigns{N: 0} }

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
	}
	for name, text := range files {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()

	cmd := exec.Command(bin, "dev", "--json", "--addr", addr)
	cmd.Dir = dir
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Signal(os.Interrupt); cmd.Wait() })
	sc := bufio.NewScanner(stdout)
	for sc.Scan() && !strings.Contains(sc.Text(), `"event":"ready"`) {
	}
	go func() {
		for sc.Scan() {
		}
	}()

	hx := filepath.Join(dir, "templates/home.hx")
	err := chromedp.Do(ctx,
		chromedp.Navigate("http://"+addr),
		waitJS(`document.querySelector("#msg") && document.querySelector("#msg").textContent === "Hello 0"`),
		chromedp.Func(func(context.Context, *chromedp.Target) error {
			return os.WriteFile(hx, []byte("component Home(n int)\n<p id=\"msg\">Bye {m}</p>\n"), 0o644)
		}),
		waitJS(`document.querySelector("#loom-dev-overlay") && document.querySelector("#loom-dev-overlay").textContent.includes("TC301")`),
		chromedp.Func(func(context.Context, *chromedp.Target) error {
			return os.WriteFile(hx, []byte("component Home(n int)\n<p id=\"msg\">Bye {n}</p>\n"), 0o644)
		}),
		// The page reloads during this wait; waitJS retries across it.
		waitJS(`document.querySelector("#msg") && document.querySelector("#msg").textContent === "Bye 0" && !document.querySelector("#loom-dev-overlay")`),
	)
	if err != nil {
		t.Fatal(err)
	}
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
