package manage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/moomerman/loom/kit"
)

const v1 = `component Badge(label string, tone: :info | :danger = :info)
  <span class={"badge" tone}>
    {label}
  </span>
end
`

const js = "export function mount(el) {\n  return {};\n}\n"

// testKit has one component, badge, with a behavior module.
func testKit() kit.Kit {
	return kit.Kit{
		FS: fstest.MapFS{
			"badge.hx":    {Data: []byte(v1)},
			"badge.js":    {Data: []byte(js)},
			"tokens.toml": {Data: []byte("[color]\naccent = \"#000\"\n")},
		},
		Catalog: []kit.Entry{{Name: "badge", Summary: "A badge", Modules: []string{"badge.js"}}},
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func add(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "components")
	res, err := Add(testKit(), dir, []string{"badge"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Status != Added || res[1].Status != TokensNew {
		t.Fatalf("results = %+v", res)
	}
	return dir
}

func TestAdd(t *testing.T) {
	dir := add(t)
	if got := read(t, filepath.Join(dir, "badge.hx")); got != v1 {
		t.Errorf("badge.hx = %q", got)
	}
	if got := read(t, filepath.Join(dir, "badge.js")); got != js {
		t.Errorf("badge.js = %q", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "tokens.toml")); err != nil {
		t.Errorf("tokens.toml not copied: %v", err)
	}
	if _, err := Add(testKit(), dir, []string{"badge"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("adding twice: %v", err)
	}
	if _, err := Add(testKit(), dir, []string{"bagde"}); err == nil || !strings.Contains(err.Error(), "has no component") {
		t.Errorf("unknown: %v", err)
	}
}

func TestCatalog(t *testing.T) {
	cs, err := Catalog(kit.Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 16 {
		t.Fatalf("%d components", len(cs))
	}
	var button *Component
	for i := range cs {
		if cs[i].Name == "button" {
			button = &cs[i]
		}
	}
	if button == nil {
		t.Fatal("no button")
	}
	params := map[string]Param{}
	for _, p := range button.Components[0].Params {
		params[p.Name] = p
	}
	if p := params["variant"]; p.Required || p.Default == "" || !strings.HasPrefix(p.Type, ":") {
		t.Errorf("variant = %+v", p)
	}
	if p := params["on:click"]; p.Required || p.Type != "event" {
		t.Errorf("on:click = %+v", p)
	}
	for _, c := range cs {
		if c.Name == "dropdown" {
			if len(c.Behaviors) != 1 || c.Behaviors[0].Name != "dropdown" || !reflect.DeepEqual(c.Behaviors[0].Emits, []string{"select", "close"}) || !reflect.DeepEqual(c.Files, []string{"dropdown.hx", "dropdown.js"}) {
				t.Errorf("dropdown = %+v", c)
			}
		}
	}
}
