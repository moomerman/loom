package tally

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The rows templates render. Templates take strings, ints, floats, bools
// and structs of those, so money, dates and colors are formatted here,
// once, and the templates only place them.

// Person is someone who can sign in and belong to groups.
type Person struct {
	ID       string
	Name     string
	First    string // "Sam"
	Initials string // "SR"
	Color    string // a color token's name: teal, violet, ...
	Email    string
}

// GroupCard is a group as the dashboard and sidebar show it, with the
// signed-in person's balance in it.
type GroupCard struct {
	ID       int
	Name     string
	Emoji    string
	Color    string
	Href     string
	Members  []Person
	Expenses int
	Balance  Amount // the signed-in person's: positive when they are owed
	Current  bool   // the sidebar's current group
}

// Amount is money formatted for display, with a tone for its color.
type Amount struct {
	Cents int
	Text  string // "$1,234.56", always positive
	Tone  string // owed, owe or settled
	Label string // "you are owed", "you owe", "settled up"
}

// ExpenseRow is one expense in a group's list.
type ExpenseRow struct {
	ID       int
	Href     string
	Month    string // "October 2026", for the month headings
	Day      string // "09"
	MonthAbr string // "Oct"
	Title    string
	Category Category
	Amount   string // "$86.42"
	PaidBy   string // "Priya paid", "You paid"
	Mine     Amount // what this expense means for the signed-in person
	MineNote string // "you lent", "you borrowed", "not involved"
	Recurs   bool
}

// MonthGroup is a month of expenses, newest first.
type MonthGroup struct {
	Label    string
	Total    string
	Expenses []ExpenseRow
}

// Category is an expense's kind, with its icon and color.
type Category struct {
	Key   string
	Label string
	Icon  string
	Color string
}

// categories are the kinds of expense, in the order the composer lists them.
var categories = []Category{
	{"general", "General", "🧾", "slate"},
	{"groceries", "Groceries", "🛒", "green"},
	{"dining", "Eating out", "🍽️", "orange"},
	{"rent", "Rent", "🔑", "indigo"},
	{"utilities", "Utilities", "💡", "amber"},
	{"household", "Household", "🧽", "teal"},
	{"travel", "Travel", "✈️", "sky"},
	{"lodging", "Lodging", "🛏️", "violet"},
	{"transport", "Transport", "🚋", "sky"},
	{"entertainment", "Entertainment", "🎭", "pink"},
	{"sport", "Sport", "🏅", "red"},
}

// category returns the category with the key, or General.
func category(key string) Category {
	for _, c := range categories {
		if c.Key == key {
			return c
		}
	}
	return categories[0]
}

// MemberBalance is one member's standing in a group.
type MemberBalance struct {
	Person Person
	Amount Amount
	Bar    int  // the bar's width, 0 to 100, relative to the largest balance
	Me     bool // the signed-in person
}

// Payment is a suggested payment that settles the group up.
type Payment struct {
	From   Person
	To     Person
	Cents  int
	Amount string
	FromMe bool // the signed-in person pays: offer to record it
	ToMe   bool // the signed-in person is paid: offer a reminder
	Key    string
}

// CategoryTotal is a category's share of a group's spending.
type CategoryTotal struct {
	Category Category
	Amount   string
	Percent  int
	Bar      int // 0 to 100, relative to the largest category
}

// RecurringRow is a monthly bill.
type RecurringRow struct {
	ID       int
	Title    string
	Category Category
	Amount   string
	PaidBy   string
	Next     string // "Nov 1"
}

// ShareRow is one person's part of an expense.
type ShareRow struct {
	Person  Person
	Amount  string
	Paid    bool // they paid the expense
	Percent int
}

// ActivityRow is an expense or settlement in a feed.
type ActivityRow struct {
	Key     string
	Href    string
	Actor   Person
	Text    string // "added Pizza night"
	Group   string // "🏠 Flat 4B"
	Amount  string
	Mine    Amount
	When    string // "2 days ago"
	Settled bool   // a settlement, not an expense
}

// Notification is an entry in the activity inbox.
type Notification struct {
	ID    int
	Body  string
	Group string
	Href  string
	When  string
	Read  bool
}

// Option is a choice in a select.
type Option struct {
	Value    string
	Label    string
	Selected bool
}

// SplitRow is a member in the expense composer's split.
type SplitRow struct {
	Person   Person
	Included bool   // split equally: is this person in it
	Share    string // split equally: the share it works out to
}

// Toast is a short message after an action, such as "Reminder sent".
type Toast struct {
	ID   int // a new id per toast, so a new one replays its animation
	Text string
	Tone string // success or info
}

// person makes a Person from its row.
func person(id, name, email, color string) Person {
	first, initials := name, ""
	if i := strings.IndexByte(name, ' '); i > 0 {
		first = name[:i]
	}
	for _, w := range strings.Fields(name) {
		initials += strings.ToUpper(w[:1])
	}
	return Person{ID: id, Name: name, First: first, Initials: initials, Color: color, Email: email}
}

// money formats cents as dollars with thousands separators: $1,234.56.
func money(cents int64) string {
	if cents < 0 {
		cents = -cents
	}
	whole, frac := cents/100, cents%100
	s := fmt.Sprintf("%d", whole)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return fmt.Sprintf("$%s.%02d", s, frac)
}

// FormatMoney formats cents as dollars, for templates.
//
//loom:helper
func FormatMoney(cents int) string { return money(int64(cents)) }

// balance describes a net balance: positive is owed to the person.
func balance(cents int64) Amount {
	a := Amount{Cents: int(cents), Text: money(cents)}
	switch {
	case cents > 0:
		a.Tone, a.Label = "owed", "you are owed"
	case cents < 0:
		a.Tone, a.Label = "owe", "you owe"
	default:
		a.Tone, a.Label = "settled", "settled up"
	}
	return a
}

// ago says how long ago t was, roughly: "just now", "3 hours ago", "Oct 2".
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	case t.Year() == now.Year():
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2, 2006")
}

// plural says "1 day", "2 days".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// parseCents reads an amount typed by a person, "12", "12.5" or
// "$1,234.56", as cents. ok is false for anything else, or a negative.
func parseCents(s string) (int64, bool) {
	s = strings.TrimSpace(strings.NewReplacer("$", "", ",", "").Replace(s))
	whole, frac, _ := strings.Cut(s, ".")
	if s == "" || len(frac) > 2 {
		return 0, false
	}
	if whole == "" {
		whole = "0"
	}
	frac = (frac + "00")[:2]
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil || w < 0 || f < 0 {
		return 0, false
	}
	return w*100 + f, true
}

// Width is a bar's inline width, for style={width(n)}: templates have
// no int-to-string conversion.
//
//loom:helper
func Width(percent int) string { return fmt.Sprintf("width: %d%%", percent) }

// UnreadText says "3 new notifications".
//
//loom:helper
func UnreadText(n int) string { return plural(n, "new notification") }
