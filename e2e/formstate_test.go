package e2e

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/e2e/templates"
)

// rowStore holds the rows, outliving the server's restarts as a
// database would, and logs each save the pages make.
type rowStore struct {
	mu    sync.Mutex
	rows  []templates.Row
	saves []string // id=title
}

func (s *rowStore) list() []templates.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]templates.Row(nil), s.rows...)
}

func (s *rowStore) save(id, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves = append(s.saves, id+"="+title)
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Title = title
		}
	}
}

func (s *rowStore) log() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.saves, " ")
}

// rowsPage is a list whose rows are each a form saving its title on
// change.
type rowsPage struct{ store *rowStore }

func (p *rowsPage) Mount(c *loom.Ctx) {}

func (p *rowsPage) Events() loom.Events {
	return loom.Events{"save": func(c *loom.Ctx, in loom.Payload) { p.store.save(in["id"], in["title"]) }}
}

func (p *rowsPage) Assigns(c *loom.Ctx) templates.RowsAssigns {
	return templates.RowsAssigns{Rows: p.store.list()}
}

// rowsServer serves the rows at a fixed address, restartable as a deploy
// restarts it: a new app, with no sessions, and the same store.
type rowsServer struct {
	t     *testing.T
	store *rowStore
	addr  string
	srv   *httptest.Server
}

func (s *rowsServer) start() {
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
	app.Live("/", &rowsPage{store: s.store}, templates.Rows)
	s.srv = &httptest.Server{Listener: l, Config: &http.Server{Handler: app}}
	s.srv.Start()
}

func (s *rowsServer) stop() {
	if s.srv != nil {
		s.srv.CloseClientConnections()
		s.srv.Close()
		s.srv = nil
	}
}

// A remount restores only what the user typed that the server didn't
// have, to the field it was typed in: rows of forms with same-named
// fields keep their own values, and no form is told of a change it
// didn't have.
func TestRemountKeepsEachRowsInput(t *testing.T) {
	s := &rowsServer{t: t, store: &rowStore{rows: []templates.Row{{ID: "1", Title: "A"}, {ID: "2", Title: "B"}, {ID: "3", Title: "C"}}}}
	s.start()
	t.Cleanup(s.stop)
	ctx := browser(t)
	logConsole(t, ctx)
	titles := `[...document.querySelectorAll("input[name=title]")].map(i => i.value).join(",")`
	remounted := chromedp.Steps(
		do(s.stop),
		do(s.start),
		waitJS(`document.getElementById("loom-root").dataset.sid !== window.oldSID && document.getElementById("loom-root").classList.contains("loom-connected")`),
		chromedp.Sleep(300*time.Millisecond), // for any change the remount sends
		waitJS(`sessionStorage.loomOutbox === "[]"`),
		js(`window.oldSID = document.getElementById("loom-root").dataset.sid`),
	)
	var untouched, typed string
	err := chromedp.Do(ctx,
		chromedp.Navigate("http://"+s.addr),
		waitFor("#loom-root.loom-connected input[name=title]"),
		js(`window.oldSID = document.getElementById("loom-root").dataset.sid`),
		remounted,
		eval(titles, &untouched),
	)
	if err != nil {
		t.Fatal(err)
	}
	if untouched != "A,B,C" || s.store.log() != "" {
		t.Errorf("after a remount with no input: titles %q, saves %q; want A,B,C and none", untouched, s.store.log())
	}

	// Row 2's title, typed but not yet changed (it has focus), is the
	// server's only after the remount.
	err = chromedp.Do(ctx,
		click(`li[data-loom-key="2"] input`),
		js(`document.activeElement.select()`),
		chromedp.KeyEvent("Bee"),
		remounted,
		eval(titles, &typed),
	)
	if err != nil {
		t.Fatal(err)
	}
	if typed != "A,Bee,C" || s.store.log() != "2=Bee" {
		t.Errorf("after a remount with row 2 typed in: titles %q, saves %q; want A,Bee,C and 2=Bee", typed, s.store.log())
	}
}

// fieldsPage has fields the server doesn't render the user's state of,
// one it does (the select's choice), and an add form.
type fieldsPage struct {
	n, added int
	choice   string
}

func (p *fieldsPage) Mount(c *loom.Ctx) { p.choice = "a" }

func (p *fieldsPage) Events() loom.Events {
	return loom.Events{
		"bump": func(*loom.Ctx, loom.Payload) { p.n++ },
		"pick": func(*loom.Ctx, loom.Payload) { p.choice = "b" },
		"add":  func(*loom.Ctx, loom.Payload) { p.added++ },
	}
}

func (p *fieldsPage) Assigns(c *loom.Ctx) templates.FieldsAssigns {
	return templates.FieldsAssigns{N: p.n, Choice: p.choice, Added: p.added}
}

// A diff keeps the state the user gave fields whose render it doesn't
// change: an opened details, a ticked checkbox, text in a field without
// focus, a select's choice. Where it changes the render, the server
// wins: the select follows the server's new choice.
func TestDiffKeepsFormState(t *testing.T) {
	app := loom.New()
	app.Live("/", &fieldsPage{}, templates.Fields)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)
	logConsole(t, ctx)
	state := `[document.querySelector("details").open, document.querySelector("[name=agree]").checked, document.querySelector(".note").value, document.querySelector("select").value].join(",")`
	var kept, picked string
	err := chromedp.Do(ctx,
		chromedp.Navigate(srv.URL),
		waitFor("#loom-root.loom-connected .bump"),
		click("summary"),
		click("[name=agree]"),
		typeInto(".note", "hello"),
		js(`document.querySelector("select").focus()`),
		chromedp.KeyEvent("c"), // picks C, as a user does
		click(".bump"),
		waitJS(`document.querySelector(".n").textContent === "1"`),
		eval(state, &kept),
		click(".pick"),
		waitJS(`document.querySelector("option[value=b]").hasAttribute("selected")`),
		eval(state, &picked),
	)
	if err != nil {
		t.Fatal(err)
	}
	if kept != "true,true,hello,c" {
		t.Errorf("after an unrelated diff: details open, checked, note, choice = %q; want true,true,hello,c", kept)
	}
	if picked != "true,true,hello,b" {
		t.Errorf("after the server picked b: %q; want true,true,hello,b", picked)
	}
}

// A submitted form is cleared, whether its field still has focus (the
// user pressed Enter) or not (they clicked the button).
func TestSubmitClearsForm(t *testing.T) {
	app := loom.New()
	app.Live("/", &fieldsPage{}, templates.Fields)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)
	logConsole(t, ctx)
	var entered, clicked string
	err := chromedp.Do(ctx,
		chromedp.Navigate(srv.URL),
		waitFor("#loom-root.loom-connected .add input"),
		typeInto(".add input", "one\n"),
		waitJS(`document.querySelector(".added").textContent === "1"`),
		eval(`document.querySelector(".add input").value`, &entered),
		typeInto(".add input", "two"),
		click(".add button"),
		waitJS(`document.querySelector(".added").textContent === "2"`),
		eval(`document.querySelector(".add input").value`, &clicked),
	)
	if err != nil {
		t.Fatal(err)
	}
	if entered != "" || clicked != "" {
		t.Errorf("after submitting: %q by Enter, %q by click; want both cleared", entered, clicked)
	}
}
