package kit_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/kit"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/render"
)

// TestCatalog checks that the catalog and the files agree.
func TestCatalog(t *testing.T) {
	hx, err := fs.Glob(kit.Files, "*.hx")
	if err != nil {
		t.Fatal(err)
	}
	all, err := fs.Glob(kit.Files, "*.js")
	if err != nil {
		t.Fatal(err)
	}
	var js []string // behaviors' modules, not the generated _hx.js
	for _, f := range all {
		if !strings.HasSuffix(f, "_hx.js") {
			js = append(js, f)
		}
	}
	listed := map[string]bool{}
	for _, e := range kit.Catalog {
		for _, f := range e.Files() {
			listed[f] = true
			if _, err := kit.Default.Source(e, f); err != nil {
				t.Errorf("%s: %v", e.Name, err)
			}
		}
	}
	for _, f := range append(hx, js...) {
		if !listed[f] {
			t.Errorf("%s isn't in kit.Catalog", f)
		}
	}
	if len(listed) != len(hx)+len(js) {
		t.Errorf("catalog lists %d files; the kit has %d", len(listed), len(hx)+len(js))
	}
}

func slot(html string) render.Slot {
	return func() *render.Rendered {
		return &render.Rendered{Statics: &render.Statics{Parts: []string{html}}}
	}
}

// TestRender snapshots each component in its main states.
func TestRender(t *testing.T) {
	click := render.Event{Name: "save", Vals: []render.Val{{Name: "id", Value: "1"}}}
	pages := []string{
		loomtest.Render(t, kit.Button, kit.ButtonAssigns{Label: "Save", OnClick: click}),
		loomtest.Render(t, kit.Button, kit.ButtonAssigns{Label: "Delete", Variant: kit.ButtonVariantDanger, Size: kit.ButtonSizeSm, Busy: true}),
		loomtest.Render(t, kit.Link, kit.LinkAssigns{Href: "/", Label: "Home", Variant: kit.LinkVariantNav, Current: true}),
		loomtest.Render(t, kit.Input, kit.InputAssigns{Name: "email", Label: "Email", Type: kit.InputTypeEmail, Error: "Required", HideLabel: true}),
		loomtest.Render(t, kit.Select, kit.SelectAssigns{Name: "size", Label: "Size", InnerBlock: slot(`<option>S</option>`)}),
		loomtest.Render(t, kit.Checkbox, kit.CheckboxAssigns{Label: "Done", Checked: true}),
		loomtest.Render(t, kit.Textarea, kit.TextareaAssigns{Name: "notes", Label: "Notes", Value: "a < b", Rows: 4}),
		loomtest.Render(t, kit.Input, kit.InputAssigns{Field: form.Field{Name: "amount", Value: "x", Error: "Enter a number."}, Label: "Amount"}),
		loomtest.Render(t, kit.Select, kit.SelectAssigns{Field: form.Field{Name: "who", Error: "Pick someone."}, Label: "Who", InnerBlock: slot(`<option>Sam</option>`)}),
		loomtest.Render(t, kit.Checkbox, kit.CheckboxAssigns{Field: form.Field{Name: "agree", Value: "true"}, Label: "Agree"}),
		loomtest.Render(t, kit.Textarea, kit.TextareaAssigns{Field: form.Field{Name: "notes", Value: "typed", Error: "Too long."}, Label: "Notes"}),
		loomtest.Render(t, kit.FormField, kit.FormFieldAssigns{Label: "Name", Error: "Too short", InnerBlock: slot(`<input name="name">`)}),
		loomtest.Render(t, kit.Card, kit.CardAssigns{Title: "Card", InnerBlock: slot("<p>Body</p>")}),
		loomtest.Render(t, kit.Badge, kit.BadgeAssigns{Label: "3", Tone: kit.BadgeToneAccent}),
		loomtest.Render(t, kit.Alert, kit.AlertAssigns{Tone: kit.AlertToneDanger, Title: "Oops", InnerBlock: slot("Try again.")}),
		loomtest.Render(t, kit.Dialog, kit.DialogAssigns{Open: true, Title: "Delete?", OnClose: render.Event{Name: "close"}, InnerBlock: slot("<p>Sure?</p>"), Actions: slot("<button>Yes</button>")}),
		loomtest.Render(t, kit.Dialog, kit.DialogAssigns{Title: "Closed"}),
		loomtest.Render(t, kit.Dropdown, kit.DropdownAssigns{Label: "Fruit", Open: true, Value: "pear", OnToggle: render.Event{Name: "toggle"}, OnSelect: render.Event{Name: "pick"}, InnerBlock: func() *render.Rendered {
			return kit.DropdownOption.Render(nil, kit.DropdownOptionAssigns{Value: "pear", Label: "Pear"})
		}}),
		loomtest.Render(t, kit.Dropdown, kit.DropdownAssigns{Label: "Closed"}),
		loomtest.Render(t, kit.Table, kit.TableAssigns{Caption: "Jobs", Head: slot("<tr><th>ID</th></tr>"), InnerBlock: slot("<tr><td>1</td></tr>")}),
		loomtest.Render(t, kit.Tabs, kit.TabsAssigns{Label: "Filter", InnerBlock: func() *render.Rendered {
			return kit.Tab.Render(nil, kit.TabAssigns{Label: "All", Selected: true, OnClick: render.Event{Name: "filter"}})
		}}),
		loomtest.Render(t, kit.Spinner, kit.SpinnerAssigns{}),
		loomtest.Render(t, kit.EmptyState, kit.EmptyStateAssigns{Title: "Nothing here", InnerBlock: slot("<p>Add one.</p>")}),
	}
	loomtest.Snapshot(t, strings.Join(pages, "\n\n"))
}

// TestAccessible checks every variant of every component against
// loomtest's accessibility rules.
func TestAccessible(t *testing.T) {
	for _, c := range render.Components() {
		if c.PackageName() == "kit" {
			t.Run(c.ComponentName(), func(t *testing.T) { loomtest.AssertPreviewsAccessible(t, c) })
		}
	}
}
