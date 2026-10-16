package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentsList(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runComponents([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var cat []struct {
		Name       string `json:"name"`
		Components []struct {
			Name string `json:"name"`
		} `json:"components"`
	}
	if err := json.Unmarshal(out.Bytes(), &cat); err != nil || len(cat) != 16 {
		t.Fatalf("catalog: %v, %d entries\n%s", err, len(cat), out.String())
	}
	out.Reset()
	runComponents(nil, &out, &errOut)
	if !strings.Contains(out.String(), "Button(") {
		t.Errorf("human output:\n%s", out.String())
	}
}

func TestComponentsAdd(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "components")
	var out, errOut bytes.Buffer
	if code := runComponents([]string{"add", "--dir", dir, "--json", "button", "badge"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errOut.String())
	}
	var res componentsOutput
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 3 || len(res.Diagnostics) != 0 {
		t.Errorf("result: %s", out.String())
	}
	for _, f := range []string{"components/button.hx", "components/button_hx.go", "tokens.toml"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "components.lock")); err == nil {
		t.Error("add wrote components.lock")
	}
	out.Reset()
	errOut.Reset()
	if code := runComponents([]string{"add", "--dir", dir, "button"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "already exists") {
		t.Errorf("adding twice: exit %d\n%s", code, errOut.String())
	}
}
