package e2e

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
)

// counter is a live layout whose count survives a restart (Keep); its
// toast doesn't.
type counter struct {
	kept  struct{ Count int }
	toast string
}

func (l *counter) Keep() any { return &l.kept }

func (l *counter) Mount(c *loom.Ctx) { l.toast = "mounted" }

func (l *counter) Assigns(c *loom.Ctx) hxtest.ChromeAssigns {
	return hxtest.ChromeAssigns{Count: l.kept.Count, Toast: l.toast}
}

func (l *counter) Events() loom.Events {
	return loom.Events{
		"bump":    func(c *loom.Ctx, _ loom.Payload) { l.kept.Count++ },
		"dismiss": func(c *loom.Ctx, _ loom.Payload) { l.toast = "" },
	}
}

type countedPage struct{}

func (p *countedPage) Mount(c *loom.Ctx) {}

func (p *countedPage) Assigns(c *loom.Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "page"}
}

// keepServer serves the counter at a fixed address, restartable as a
// deploy restarts it: a new app, with no sessions, and the same secret.
type keepServer struct {
	t    *testing.T
	addr string
	srv  *httptest.Server
}

func (s *keepServer) start() {
	addr := s.addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		s.t.Fatal(err)
	}
	s.addr = l.Addr().String()
	app := loom.New(loom.Config{Secret: strings.Repeat("k", 32)})
	app.Group(loom.LiveLayout(hxtest.Chrome, &counter{})).Live("/", &countedPage{}, hxtest.Counter)
	s.srv = &httptest.Server{Listener: l, Config: &http.Server{Handler: app}}
	s.srv.Start()
}

func (s *keepServer) stop() {
	if s.srv != nil {
		s.srv.CloseClientConnections()
		s.srv.Close()
		s.srv = nil
	}
}

// What a live layout keeps comes back when the page remounts on the
// restarted server; what it doesn't keep starts over from Mount.
func TestKeepAcrossRestart(t *testing.T) {
	s := &keepServer{t: t}
	s.start()
	t.Cleanup(s.stop)
	ctx := browser(t)
	logConsole(t, ctx)
	var sid, count, toast string
	err := chromedp.Run(ctx,
		chromedp.Navigate("http://"+s.addr),
		waitFor("#loom-root.loom-connected button.bump"),
		click("span.toast"),
		click("button.bump"),
		click("button.bump"),
		waitJS(`document.querySelector("button.bump").textContent === "2" && sessionStorage.loomOutbox === "[]"`),
		eval(`window.oldSID = document.getElementById("loom-root").dataset.sid`, &sid),
		do(s.stop),
		do(s.start),
		waitJS(`document.getElementById("loom-root").dataset.sid !== window.oldSID && document.getElementById("loom-root").classList.contains("loom-connected")`),
		eval(`document.querySelector("button.bump").textContent`, &count),
		eval(`(document.querySelector("span.toast") || {}).textContent || ""`, &toast),
		click("button.bump"),
		waitJS(`document.querySelector("button.bump").textContent === "3"`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != "2" || toast != "mounted" {
		t.Errorf("after the restart: count %q, toast %q; want the kept count 2, and the toast Mount sets", count, toast)
	}
}
