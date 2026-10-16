package devmail

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/moomerman/loom"
)

// MailboxPage is the mailbox: the kept mail, newest first, beside the
// open mail. At "/" the open mail is the newest, so a person waiting for
// a sign-in link sees it arrive; at "/{id}" it is that mail, kept open
// while more arrive.
type MailboxPage struct {
	app *App
	id  int // the mail in the path; 0 at "/", for the newest
}

// MailRow is a mail in the list.
type MailRow struct {
	ID      string
	To      string
	Subject string
	When    string // e.g. "3m ago"
	Current bool   // the open mail
}

// Part is a piece of a mail's text: a link if Href is set, else text.
// The text is split in Go so the template can render links as links
// without parsing anything.
type Part struct {
	Seq  int // its place in the text: its :key
	Text string
	Href string
}

// Link is a link in the open mail, offered as a button: following it is
// usually why the mail was opened.
type Link struct {
	Href string
}

// Mount reads the mail id from the path and subscribes to the outbox,
// which broadcasts each mail it keeps.
func (p *MailboxPage) Mount(c *loom.Ctx) {
	p.id, _ = strconv.Atoi(c.Request.PathValue("id"))
	c.Title("Mail")
	c.Subscribe(loom.MailTopic)
}

// Events lists the page's one action. Clearing loses nothing that
// matters in dev, so it doesn't ask first.
func (p *MailboxPage) Events() loom.Events {
	return loom.Events{"clear": func(c *loom.Ctx, _ loom.Payload) { p.app.app.ClearSentMail() }}
}

// HandleInfo has nothing to change: Assigns reads the outbox on every
// render, and it holds at most 50 mails.
func (p *MailboxPage) HandleInfo(c *loom.Ctx, msg loom.Message) {}

// Assigns reads the outbox for the template.
func (p *MailboxPage) Assigns(c *loom.Ctx) MailboxAssigns {
	kept := p.app.app.SentMail()
	now := time.Now()
	a := MailboxAssigns{Count: len(kept)}
	var open *loom.SentMail
	for i := len(kept) - 1; i >= 0; i-- { // newest first
		m := &kept[i]
		if open == nil && (p.id == 0 || m.ID == p.id) {
			open = m
		}
		a.Mail = append(a.Mail, MailRow{
			ID: strconv.Itoa(m.ID), To: m.To, Subject: subject(m.Subject), When: ago(now, m.Time), Current: open == m,
		})
	}
	if open == nil {
		if p.id != 0 {
			a.Missing = strconv.Itoa(p.id)
		}
		return a
	}
	c.Title(subject(open.Subject))
	a.Open = true
	a.ID, a.To, a.Subject, a.At = strconv.Itoa(open.ID), open.To, subject(open.Subject), open.Time.UTC().Format("2006-01-02 15:04:05 UTC")
	a.Body = parts(open.Text)
	a.HTML = open.HTML
	for _, href := range links(open.Text + "\n" + open.HTML) {
		a.Links = append(a.Links, Link{Href: href})
	}
	return a
}

// subject stands in for an empty subject, which would leave a blank row.
func subject(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no subject)"
	}
	return s
}

// linkRE matches the links a person would click: http and https only,
// so a mail can't put a javascript: URL in an href.
var linkRE = regexp.MustCompile(`https?://[^\s<>"']+`)

// trailing is punctuation that ends a sentence after a link rather than
// belonging to it, as in "open https://app.test/x."
const trailing = ".,;:!?)]"

// parts splits text into text and links.
func parts(text string) []Part {
	var out []Part
	for len(text) > 0 {
		loc := linkRE.FindStringIndex(text)
		if loc == nil {
			out = append(out, Part{Text: text})
			break
		}
		href := strings.TrimRight(text[loc[0]:loc[1]], trailing)
		if loc[0] > 0 {
			out = append(out, Part{Text: text[:loc[0]]})
		}
		out = append(out, Part{Text: href, Href: href})
		text = text[loc[0]+len(href):]
	}
	for i := range out {
		out[i].Seq = i
	}
	return out
}

// links returns the distinct links in s, in order.
func links(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range linkRE.FindAllString(s, -1) {
		href := strings.TrimRight(raw, trailing)
		if !seen[href] {
			seen[href] = true
			out = append(out, href)
		}
	}
	return out
}

// ago formats t relative to now: "12s ago".
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
