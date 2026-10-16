package loomtest

import (
	"os"
	"strings"
	"testing"

	"github.com/moomerman/loom"
)

// ManifestFile is the file Manifest keeps: the app's manifest, committed
// next to the test that checks it.
const ManifestFile = "loom.manifest"

// Manifest fails the test if the app's behaviour no longer matches the
// committed loom.manifest in the test's package directory (see
// docs/manifest.md). `go test -update` rewrites the file; commit it with
// the change, so the pull request shows reviewers what the app now does:
// a new route, an event, a watched table, a job.
//
//	func TestManifest(t *testing.T) {
//		app := loom.New()
//		todo.Setup(app)
//		loomtest.Manifest(t, app)
//	}
//
// The manifest includes the migrations, from the nearest directory named
// migrations, found as DB finds it.
func Manifest(t testing.TB, app *loom.App) {
	t.Helper()
	if err := checkGenerated(); err != nil {
		t.Fatal(err) // the manifest comes from the generated code
	}
	m := app.Manifest()
	if dir := findMigrations(); dir != "" {
		if err := m.AddMigrations(os.DirFS(dir)); err != nil {
			t.Fatal(err)
		}
	}
	got := m.Text()
	if *update {
		if err := os.WriteFile(ManifestFile, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(ManifestFile)
	if os.IsNotExist(err) {
		t.Fatalf("no %s; run go test -update to create it, and commit it", ManifestFile)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("the app's behaviour changed, so %s is out of date (run go test -update and commit it with your change):\n%s", ManifestFile, manifestDiff(string(want), got))
	}
}

// manifestDiff lists the lines removed from want (-) and added in got
// (+), from a longest common subsequence: every change, since one
// feature usually touches several sections.
func manifestDiff(want, got string) string {
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			out.WriteString("- " + a[i] + "\n")
			i++
		default:
			out.WriteString("+ " + b[j] + "\n")
			j++
		}
	}
	return out.String()
}
