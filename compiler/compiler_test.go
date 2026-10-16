package compiler

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/moomerman/loom/compiler/gogen"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestGeneratedCode regenerates the templates in internal/hxtest and
// compares the output with the committed *_hx.go files, which serve as
// golden files for the generator. internal/hxtest's own tests check that
// the committed code compiles and renders the expected HTML. It also
// checks the framework's default root layout is up to date.
func TestGeneratedCode(t *testing.T) {
	res, err := Check("../internal/hxtest", "../internal/subapptest", "../default_root.hx")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) > 0 {
		t.Fatal(res.Diagnostics)
	}
	for _, pkg := range res.Packages {
		type output struct {
			path string
			src  []byte
		}
		var outs []output
		for _, f := range pkg.Files {
			got, err := gogen.File(pkg.Info, f)
			if err != nil {
				t.Fatal(err)
			}
			outs = append(outs, output{gogen.OutputName(f.Source.Name), got})
		}
		if src := gogen.Tokens(pkg.Info, pkg.Tokens); src != nil {
			outs = append(outs, output{filepath.Join(pkg.Dir, gogen.TokensFile), src})
		}
		for _, o := range outs {
			path, got := o.path, o.src
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is out of date; run go test ./compiler -update (or loom build ./internal/hxtest) and review the diff\n--- got ---\n%s", path, got)
			}
		}
	}
}

func TestCheckReportsParseAndTypeErrors(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/a.hx", []byte("component A()\n<p>{}</p>\n"), 0o644)
	os.WriteFile(dir+"/b.hx", []byte("component B()\n<p>fine</p>\n"), 0o644)
	res, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 2 || len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != "PA219" {
		t.Fatalf("unexpected result: checked %d, %v", res.Checked, res.Diagnostics)
	}
	if res.Packages[0].Info != nil {
		t.Error("a package with parse errors should not be type-checked")
	}
}

func TestStaleFindsGeneratedFilesBuildWouldChange(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "views")
	os.Mkdir(dir, 0o755)
	os.WriteFile(dir+"/a.hx", []byte("component A()\n<p>one</p>\n"), 0o644)
	if _, stale, err := Stale(dir); err != nil || len(stale) == 0 {
		t.Fatalf("before any build: stale %v, %v; want a_hx.go", stale, err)
	}
	if _, _, err := Build(dir); err != nil {
		t.Fatal(err)
	}
	if _, stale, err := Stale(dir); err != nil || len(stale) != 0 {
		t.Fatalf("after a build: stale %v, %v; want none", stale, err)
	}
	os.WriteFile(dir+"/a.hx", []byte("component A()\n<p>two</p>\n"), 0o644)
	_, stale, err := Stale(dir)
	if err != nil || len(stale) != 1 || filepath.Base(stale[0]) != "a_hx.go" {
		t.Fatalf("after an edit: stale %v, %v; want a_hx.go", stale, err)
	}
}

func TestFindTemplatesListsOverlappingPathsOnce(t *testing.T) {
	files, err := FindTemplates("../internal/hxtest", "../internal/hxtest/parts")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(slices.Compact(slices.Clone(files))) || !slices.IsSorted(files) {
		t.Fatalf("files listed twice: %v", files)
	}
}
