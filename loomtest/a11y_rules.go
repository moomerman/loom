package loomtest

import (
	"fmt"
	"sort"
	"strings"
)

// A11yRules are the built-in accessibility rules, checked by A11y along
// with duplicate-id. The IDs follow axe-core's where one matches.
var A11yRules = []A11yRule{
	{
		ID: "image-alt", Selector: "img",
		Fix: `add alt="..." describing the image, or alt="" if it is decoration`,
		Check: func(e *Element) string {
			if _, ok := e.attrs["alt"]; ok || isPresentational(e) {
				return ""
			}
			return "image has no alt text"
		},
	},
	{
		ID: "button-name", Selector: "button",
		Fix:   `give the button text, or aria-label="..." if it shows only an icon`,
		Check: needsName("button"),
	},
	{
		ID: "button-name", Selector: "[role=button]",
		Fix:   `give the button text, or aria-label="..." if it shows only an icon`,
		Check: needsName("button"),
	},
	{
		ID: "link-name", Selector: "a[href]",
		Fix:   `give the link text, or aria-label="..." if it shows only an icon`,
		Check: needsName("link"),
	},
	{
		ID: "label", Selector: "input",
		Fix:   `wrap the control in a <label>, point a <label for="id"> at it, or give it aria-label="..."`,
		Check: needsLabel,
	},
	{
		ID: "label", Selector: "select",
		Fix:   `wrap the control in a <label>, point a <label for="id"> at it, or give it aria-label="..."`,
		Check: needsLabel,
	},
	{
		ID: "label", Selector: "textarea",
		Fix:   `wrap the control in a <label>, point a <label for="id"> at it, or give it aria-label="..."`,
		Check: needsLabel,
	},
	{
		ID: "input-image-alt", Selector: "input[type=image]",
		Fix: `add alt="..." saying what the button does`,
		Check: func(e *Element) string {
			if strings.TrimSpace(e.attrs["alt"]) == "" && Name(e) == "" {
				return "image button has no alt text"
			}
			return ""
		},
	},
	{
		ID: "dialog-name", Selector: "[role=dialog]",
		Fix:   `give the dialog aria-label="..." or aria-labelledby="<id of its heading>"`,
		Check: needsName("dialog"),
	},
	{
		ID: "dialog-name", Selector: "[role=alertdialog]",
		Fix:   `give the dialog aria-label="..." or aria-labelledby="<id of its heading>"`,
		Check: needsName("dialog"),
	},
	{
		ID: "dialog-name", Selector: "dialog",
		Fix:   `give the dialog aria-label="..." or aria-labelledby="<id of its heading>"`,
		Check: needsName("dialog"),
	},
	{
		ID: "aria-roles", Selector: "[role]",
		Fix: "use a role from WAI-ARIA, e.g. button, dialog, listbox, option, tab, tablist, tabpanel, menu, menuitem, status, alert",
		Check: func(e *Element) string {
			for _, r := range strings.Fields(e.attrs["role"]) {
				if !ariaRoles[r] {
					return fmt.Sprintf("role %q isn't an ARIA role", r)
				}
			}
			if strings.TrimSpace(e.attrs["role"]) == "" {
				return "role is empty"
			}
			return ""
		},
	},
	{
		ID: "aria-valid-attr", Selector: "*",
		Fix: "use an aria-* attribute from WAI-ARIA, spelled in full, e.g. aria-label, aria-labelledby, aria-describedby, aria-expanded, aria-selected, aria-hidden",
		Check: func(e *Element) string {
			var bad []string
			for k := range e.attrs {
				if strings.HasPrefix(k, "aria-") && ariaAttrs[k] == "" {
					bad = append(bad, k)
				}
			}
			if len(bad) > 0 {
				sort.Strings(bad)
				return fmt.Sprintf("%s isn't an ARIA attribute", strings.Join(bad, ", "))
			}
			return ""
		},
	},
	{
		ID: "aria-valid-attr-value", Selector: "*",
		Fix: `write true or false (aria-checked and aria-pressed also take "mixed"; aria-current also page, step, location, date, time)`,
		Check: func(e *Element) string {
			var bad []string
			for k, v := range e.attrs {
				var ok bool
				switch ariaAttrs[k] {
				case "bool":
					ok = v == "true" || v == "false"
				case "tristate":
					ok = v == "true" || v == "false" || v == "mixed"
				case "current":
					ok = strings.Contains(" true false page step location date time ", " "+v+" ")
				default:
					ok = true
				}
				if !ok {
					bad = append(bad, fmt.Sprintf("%s=%q", k, v))
				}
			}
			if len(bad) > 0 {
				sort.Strings(bad)
				return "invalid value " + strings.Join(bad, ", ")
			}
			return ""
		},
	},
	{
		ID: "aria-reference", Selector: "*",
		Fix: "point the attribute at the id of an element on the page, or remove it",
		Check: func(e *Element) string {
			var bad []string
			for _, k := range []string{"aria-labelledby", "aria-describedby", "aria-controls", "aria-owns", "aria-activedescendant", "aria-errormessage"} {
				for _, id := range strings.Fields(e.attrs[k]) {
					if e.ByID(id) == nil {
						bad = append(bad, fmt.Sprintf("%s=%q", k, id))
					}
				}
			}
			if e.tag == "label" {
				if id := e.attrs["for"]; id != "" && e.ByID(id) == nil {
					bad = append(bad, fmt.Sprintf("for=%q", id))
				}
			}
			if len(bad) > 0 {
				return "no element has the id in " + strings.Join(bad, ", ")
			}
			return ""
		},
	},
	{
		ID: "aria-required-parent", Selector: "[role]",
		Fix: "put the element inside its container role: option in listbox, tab in tablist, menuitem in menu or menubar, listitem in list, row in table or grid",
		Check: func(e *Element) string {
			parents := requiredParents[e.attrs["role"]]
			if parents == nil || e.Parent() == nil {
				// At the top of a fragment, such as a component rendered
				// alone, the container is the caller's to provide.
				return ""
			}
			for a := e.Parent(); a != nil; a = a.Parent() {
				if r := a.attrs["role"]; r != "" && (parents[r] || r == "group") {
					return ""
				}
				if r := implicitRole(a); parents[r] {
					return ""
				}
			}
			var names []string
			for p := range parents {
				names = append(names, p)
			}
			sort.Strings(names)
			return fmt.Sprintf("role %s must be inside %s", e.attrs["role"], strings.Join(names, " or "))
		},
	},
	{
		ID: "html-lang", Selector: "html",
		Fix: `add lang="en" (or the page's language) to <html>`,
		Check: func(e *Element) string {
			if strings.TrimSpace(e.attrs["lang"]) == "" {
				return "the document has no language"
			}
			return ""
		},
	},
}

// needsName returns a check that an element has an accessible name,
// unless it is hidden.
func needsName(what string) func(e *Element) string {
	return func(e *Element) string {
		if hidden(e) || Name(e) != "" {
			return ""
		}
		return what + " has no accessible name"
	}
}

func needsLabel(e *Element) string {
	if !isControl(e) || hidden(e) || Name(e) != "" {
		return ""
	}
	return fmt.Sprintf("<%s> has no label", e.tag)
}

// hidden reports whether e or an ancestor is hidden from assistive
// technology.
func hidden(e *Element) bool {
	for a := e; a != nil; a = a.Parent() {
		if a.attrs["aria-hidden"] == "true" {
			return true
		}
		if _, ok := a.attrs["hidden"]; ok {
			return true
		}
	}
	return false
}

func isPresentational(e *Element) bool {
	r := e.attrs["role"]
	return r == "presentation" || r == "none" || e.attrs["aria-hidden"] == "true"
}

// implicitRole is the role an element has without a role attribute, for
// the elements that contain owned roles.
func implicitRole(e *Element) string {
	switch e.tag {
	case "ul", "ol":
		return "list"
	case "table":
		return "table"
	case "select":
		return "listbox"
	}
	return ""
}

var requiredParents = map[string]map[string]bool{
	"option":           {"listbox": true},
	"tab":              {"tablist": true},
	"menuitem":         {"menu": true, "menubar": true},
	"menuitemcheckbox": {"menu": true, "menubar": true},
	"menuitemradio":    {"menu": true, "menubar": true},
	"listitem":         {"list": true, "directory": true},
	"treeitem":         {"tree": true},
	"row":              {"table": true, "grid": true, "treegrid": true, "rowgroup": true},
}

var ariaRoles = map[string]bool{}

func init() {
	for _, r := range strings.Fields(`alert alertdialog application article banner blockquote button caption cell checkbox
		code columnheader combobox complementary contentinfo definition deletion dialog directory document emphasis
		feed figure form generic grid gridcell group heading img insertion link list listbox listitem log main marquee
		math menu menubar menuitem menuitemcheckbox menuitemradio meter navigation none note option paragraph
		presentation progressbar radio radiogroup region row rowgroup rowheader scrollbar search searchbox separator
		slider spinbutton status strong subscript superscript switch tab table tablist tabpanel term textbox time timer
		toolbar tooltip tree treegrid treeitem`) {
		ariaRoles[r] = true
	}
}

// ariaAttrs maps each ARIA attribute to the kind of value it takes:
// "bool", "tristate", "current", or "any".
var ariaAttrs = map[string]string{
	"aria-activedescendant": "any", "aria-atomic": "bool", "aria-autocomplete": "any", "aria-braillelabel": "any",
	"aria-brailleroledescription": "any", "aria-busy": "bool", "aria-checked": "tristate", "aria-colcount": "any",
	"aria-colindex": "any", "aria-colindextext": "any", "aria-colspan": "any", "aria-controls": "any",
	"aria-current": "current", "aria-describedby": "any", "aria-description": "any", "aria-details": "any",
	"aria-disabled": "bool", "aria-errormessage": "any", "aria-expanded": "bool", "aria-flowto": "any",
	"aria-haspopup": "any", "aria-hidden": "bool", "aria-invalid": "any", "aria-keyshortcuts": "any",
	"aria-label": "any", "aria-labelledby": "any", "aria-level": "any", "aria-live": "any", "aria-modal": "bool",
	"aria-multiline": "bool", "aria-multiselectable": "bool", "aria-orientation": "any", "aria-owns": "any",
	"aria-placeholder": "any", "aria-posinset": "any", "aria-pressed": "tristate", "aria-readonly": "bool",
	"aria-relevant": "any", "aria-required": "bool", "aria-roledescription": "any", "aria-rowcount": "any",
	"aria-rowindex": "any", "aria-rowindextext": "any", "aria-rowspan": "any", "aria-selected": "bool",
	"aria-setsize": "any", "aria-sort": "any", "aria-valuemax": "any", "aria-valuemin": "any", "aria-valuenow": "any",
	"aria-valuetext": "any",
}
