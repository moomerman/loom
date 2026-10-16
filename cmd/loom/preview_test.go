package main

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moomerman/loom/preview"
)

func TestPreview(t *testing.T) {
	url := devApp(t, true)
	code, out, _ := runCmd(runPreview, "--url", url)
	if code != 0 || !strings.Contains(out, "hxtest.Button") || !strings.Contains(out, "none: columns is [][]Card") {
		t.Errorf("preview: %d %q", code, out)
	}
	code, out, _ = runCmd(runPreview, "--url", url, "--json")
	if code != 0 || !strings.Contains(out, "\"component\": \"hxtest.Button\",\n    \"variants\": [\n      \"default\",\n      \"busy\",") {
		t.Errorf("preview --json: %d %q", code, out)
	}

	code, _, errOut := runCmd(runPreview, "screenshot", "--url", url, "Board")
	if code != 2 || !strings.Contains(errOut, "hxtest.Board has no previews: columns is [][]Card") {
		t.Errorf("screenshot of a component without previews: %d %q", code, errOut)
	}
	code, _, errOut = runCmd(runPreview, "screenshot", "--url", url, "Button")
	if code != 2 || !strings.Contains(errOut, `"Button" is ambiguous`) {
		t.Errorf("screenshot of an ambiguous component: %d %q", code, errOut)
	}
	code, _, errOut = runCmd(runPreview, "screenshot", "--url", url, "--variant", "nope", "hxtest.Button")
	if code != 2 || !strings.Contains(errOut, `hxtest.Button has no variant "nope"`) {
		t.Errorf("screenshot of an unknown variant: %d %q", code, errOut)
	}
	if _, err := preview.Chrome(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	code, out, errOut = runCmd(runPreview, "screenshot", "--url", url, "--json", "--out", dir, "--variant", "size=sm busy", "--theme", "dark", "hxtest.Button")
	var res struct{ Screenshots []Screenshot }
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || len(res.Screenshots) != 1 {
		t.Fatalf("screenshot: %d %q %q", code, out, errOut)
	}
	s := res.Screenshots[0]
	if want := filepath.Join(dir, "hxtest.Button.size_sm_busy.dark.png"); s.File != want || s.Theme != "dark" || s.Width == 0 {
		t.Errorf("screenshot = %+v, want file %s", s, want)
	}
	f, err := os.Open(s.File)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil || img.Bounds().Dx() != s.Width || img.Bounds().Dy() != s.Height {
		t.Errorf("PNG: %v, size %v, want %dx%d", err, img.Bounds().Size(), s.Width, s.Height)
	}

	all := t.TempDir()
	code, out, errOut = runCmd(runPreview, "screenshot", "--url", url, "--out", all, "--theme", "light")
	if code != 0 || !strings.Contains(out, "hxtest.Button.light.png") || !strings.Contains(out, "kit.Dialog.light.png") || strings.Contains(out, "Board") {
		t.Errorf("screenshot of every component: %d %q %q", code, out, errOut)
	}
}
