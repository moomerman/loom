package e2e

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/todo"
	"github.com/moomerman/loom/loomtest"
)

// server serves the todo app at a fixed address and can be restarted
// there, as a deploy restarts it: a new process, with no sessions.
type server struct {
	t    *testing.T
	db   *loom.DB
	addr string
	srv  *httptest.Server
}

func newServer(t *testing.T) *server {
	s := &server{t: t, db: loomtest.DB(t, loomtest.Committed(), loomtest.Migrations("../examples/todo/migrations"))}
	s.start()
	t.Cleanup(s.stop)
	return s
}

func (s *server) url() string { return "http://" + s.addr }

func (s *server) start() {
	addr := s.addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		s.t.Fatal(err)
	}
	s.addr = l.Addr().String()
	app := loom.New()
	app.UseDB(s.db)
	todo.Setup(app)
	s.srv = &httptest.Server{Listener: l, Config: &http.Server{Handler: app}}
	s.srv.Start()
}

func (s *server) stop() {
	if s.srv != nil {
		s.srv.CloseClientConnections()
		s.srv.Close()
		s.srv = nil
	}
}

// do runs f as a chromedp action.
func do(f func()) chromedp.Action {
	return chromedp.ActionFunc(func(context.Context) error { f(); return nil })
}

// A todo ticked while the server is down is saved once it is back: the
// event waits in the outbox, the page remounts on the new server, and
// the event is sent to its new session.
func TestRestartMidEvent(t *testing.T) {
	s := newServer(t)
	ctx := browser(t)
	logConsole(t, ctx)
	first := `li[data-loom-key="00000000-0000-0000-0000-000000000001"]`
	var sid, after string
	err := chromedp.Run(ctx,
		chromedp.Navigate(s.url()),
		waitFor("#loom-root.loom-connected .todo-list li"),
		eval(`document.getElementById("loom-root").dataset.sid`, &sid),
		do(s.stop),
		click(first+` input[type=checkbox]`),
		chromedp.Sleep(300*time.Millisecond), // the send fails and waits to retry
		do(s.start),
		waitJS(`document.querySelector(`+quote(first)+`).classList.contains("done")`),
		eval(`document.getElementById("loom-root").dataset.sid`, &after),
	)
	if err != nil {
		t.Fatal(err)
	}
	if after == sid {
		t.Error("the page kept its old session; it should have remounted on the new server")
	}
	done, err := loom.Select[bool](context.Background(), s.db, `SELECT done FROM todos ORDER BY id`)
	if err != nil || len(done) != 2 || !done[0] || done[1] {
		t.Fatalf("todos done: %v, %v; want the first ticked once", done, err)
	}
}

// Text typed into a form is kept across a restart, and submitting it
// afterwards works.
func TestRestartMidForm(t *testing.T) {
	s := newServer(t)
	ctx := browser(t)
	logConsole(t, ctx)
	var sid, typed string
	err := chromedp.Run(ctx,
		chromedp.Navigate(s.url()),
		waitFor("#loom-root.loom-connected .todo-list li"),
		eval(`document.getElementById("loom-root").dataset.sid`, &sid),
		typeInto(".new-todo input", "Buy bread"),
		do(s.stop),
		do(s.start),
		waitJS(`document.getElementById("loom-root").dataset.sid !== `+quote(sid)+` && document.getElementById("loom-root").classList.contains("loom-connected")`),
		eval(`document.querySelector(".new-todo input").value`, &typed),
		typeInto(".new-todo input", "\n"),
		waitJS(`document.querySelectorAll(".todo-list li").length === 3`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if typed != "Buy bread" {
		t.Errorf("after the restart the input has %q; want what was typed", typed)
	}
	n, err := loom.Get[int](context.Background(), s.db, `SELECT count(*) FROM todos WHERE title = 'Buy bread'`)
	if err != nil || n != 1 {
		t.Fatalf("todos titled Buy bread: %d, %v", n, err)
	}
}
