package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	events := strings.Join([]string{
		`{"Action":"start","Package":"example.com/app"}`,
		`{"Action":"run","Package":"example.com/app","Test":"TestOK"}`,
		`{"Action":"output","Package":"example.com/app","Test":"TestOK","Output":"=== RUN   TestOK\n"}`,
		`{"Action":"pass","Package":"example.com/app","Test":"TestOK","Elapsed":0}`,
		`{"Action":"run","Package":"example.com/app","Test":"TestBad"}`,
		`{"Action":"output","Package":"example.com/app","Test":"TestBad","Output":"    app_test.go:9: got 1, want 2\n"}`,
		`{"Action":"fail","Package":"example.com/app","Test":"TestBad","Elapsed":0}`,
		`{"Action":"fail","Package":"example.com/app","Elapsed":0.2}`,
		`{"ImportPath":"example.com/app/broken [example.com/app/broken.test]","Action":"build-output","Output":"broken/x.go:3:1: syntax error\n"}`,
		`{"Action":"start","Package":"example.com/app/broken"}`,
		`{"Action":"output","Package":"example.com/app/broken","Output":"FAIL\texample.com/app/broken [build failed]\n"}`,
		`{"Action":"fail","Package":"example.com/app/broken","Elapsed":0}`,
		`{"Action":"skip","Package":"example.com/app/templates","Elapsed":0}`,
	}, "\n")
	got := summarize(strings.NewReader(events))
	b, _ := json.Marshal(got)
	want := `[{"package":"example.com/app","result":"fail","elapsed":0.2,"failures":[{"test":"TestBad","output":"    app_test.go:9: got 1, want 2\n"}]},` +
		`{"package":"example.com/app/broken","result":"build-fail","elapsed":0,"failures":[{"test":"","output":"broken/x.go:3:1: syntax error\nFAIL\texample.com/app/broken [build failed]\n"}]},` +
		`{"package":"example.com/app/templates","result":"skip","elapsed":0,"failures":[]}]`
	if string(b) != want {
		t.Errorf("summary:\n%s\nwant:\n%s", b, want)
	}
}

func TestLoomTest(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an app")
	}
	dir := testApp(t)
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(`package main

import (
	"testing"

	"example.com/app/templates"
)

func TestHome(t *testing.T) {
	if (templates.HomeAssigns{N: 1}).N != 1 {
		t.Fatal("no")
	}
}

func TestFails(t *testing.T) { t.Error("this one fails") }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The generated code doesn't exist yet: loom test builds it first.
	code, out, errOut := runCmd(runTest, "--json")
	var res testOutput
	if err := json.Unmarshal([]byte(out), &res); code != 1 || err != nil {
		t.Fatalf("loom test --json: %d %s %s", code, out, errOut)
	}
	if res.Passed || len(res.Written) != 1 || len(res.Packages) != 2 || res.Packages[0].Result != "fail" ||
		len(res.Packages[0].Failures) != 1 || res.Packages[0].Failures[0].Test != "TestFails" || !strings.Contains(res.Packages[0].Failures[0].Output, "this one fails") {
		t.Errorf("loom test --json: %s", out)
	}

	code, out, _ = runCmd(runTest, "--json", "--run", "TestHome")
	if err := json.Unmarshal([]byte(out), &res); code != 0 || err != nil || !res.Passed {
		t.Errorf("loom test --run TestHome: %d %s", code, out)
	}

	// A template error stops it before go test.
	if err := os.WriteFile(filepath.Join(dir, "templates/home.hx"), []byte("component Home(n int)\n<p>{m}</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCmd(runTest, "--json")
	if err := json.Unmarshal([]byte(out), &res); code != 1 || err != nil || res.Passed || len(res.Packages) != 0 || len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != "TC301" {
		t.Errorf("loom test with a template error: %d %s", code, out)
	}
	code, out, _ = runCmd(runTest)
	if code != 1 || !strings.Contains(out, "TC301") {
		t.Errorf("loom test with a template error, for people: %d %s", code, out)
	}
}

func TestGoTestArgs(t *testing.T) {
	got := strings.Join(goTestArgs(true, "TestX", true, nil), " ")
	if got != "test -json -race -run TestX ./..." {
		t.Errorf("go test args: %q", got)
	}
}
