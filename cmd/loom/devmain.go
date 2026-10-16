package main

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/moomerman/loom"
)

// devDirective marks the main package `loom dev` runs when cmd/ holds
// more than one: a line of its own in any of the package's files.
const devDirective = "//loom:dev"

// findDevMain picks the main package `loom dev` runs when it's given
// none, so an app starts with a bare `loom dev`. Go apps keep main in
// cmd/NAME, so it looks there:
//
//  1. root itself, if it is a main package (a one-package app);
//  2. else the main packages in root/cmd/*: the one marked //loom:dev;
//  3. else the only one;
//  4. else the only one that serves a Loom app (calls loom.New and Run).
//
// Anything else is RT451, naming the candidates and the fix. The path
// returned is relative to root, as `go build` takes it ("./cmd/retro").
func findDevMain(root string) (string, error) {
	if isMain(root) {
		return ".", nil
	}
	var mains []string
	entries, _ := os.ReadDir(filepath.Join(root, "cmd"))
	for _, e := range entries {
		if e.IsDir() && isMain(filepath.Join(root, "cmd", e.Name())) {
			mains = append(mains, "./cmd/"+e.Name())
		}
	}
	sort.Strings(mains)
	if len(mains) == 0 {
		return "", &loom.Error{Code: loom.RT451,
			Msg: "loom dev found no main package to run: . isn't one, and there is none in cmd/",
			Fix: "run loom dev from the app's directory, or name the package: loom dev ./path/to/main"}
	}
	pick := func(keep func(string) bool) []string {
		var out []string
		for _, m := range mains {
			if keep(filepath.Join(root, m)) {
				out = append(out, m)
			}
		}
		return out
	}
	marked := pick(hasDevDirective)
	switch {
	case len(marked) == 1:
		return marked[0], nil
	case len(marked) > 1:
		return "", &loom.Error{Code: loom.RT451,
			Msg: "more than one main package is marked " + devDirective + ": " + strings.Join(marked, ", "),
			Fix: "keep " + devDirective + " in the one loom dev should run, or name it: loom dev " + marked[0]}
	case len(mains) == 1:
		return mains[0], nil
	}
	if serving := pick(servesLoomApp); len(serving) == 1 {
		return serving[0], nil
	}
	return "", &loom.Error{Code: loom.RT451,
		Msg: "loom dev can't tell which main package runs the app: " + strings.Join(mains, ", "),
		Fix: "add a " + devDirective + " line to the app's main.go, or name it: loom dev " + mains[0]}
}

// isMain reports whether dir holds a main package, honouring build
// constraints as go build does.
func isMain(dir string) bool {
	p, err := build.Default.ImportDir(dir, 0)
	return err == nil && p.Name == "main"
}

// hasDevDirective reports whether a file of the package in dir has a
// //loom:dev line.
func hasDevDirective(dir string) bool {
	for _, f := range goFiles(dir) {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			if strings.TrimSpace(line) == devDirective {
				return true
			}
		}
	}
	return false
}

// servesLoomApp reports whether the package in dir calls loom.New and a
// Run method: it makes a Loom app and serves it, as main must for
// loom dev, rather than being a tool that only uses Loom.
func servesLoomApp(dir string) bool {
	var news, runs bool
	fset := token.NewFileSet()
	for _, f := range goFiles(dir) {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			continue
		}
		name := ""
		for _, imp := range file.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == "github.com/moomerman/loom" {
				name = "loom"
				if imp.Name != nil {
					name = imp.Name.Name
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && name != "" && id.Name == name && sel.Sel.Name == "New" {
					news = true
				}
				if sel.Sel.Name == "Run" {
					runs = true
				}
			}
			return true
		})
	}
	return news && runs
}

// goFiles lists the non-test Go files go build would compile in dir.
func goFiles(dir string) []string {
	p, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		return nil
	}
	files := make([]string, len(p.GoFiles))
	for i, f := range p.GoFiles {
		files[i] = filepath.Join(dir, f)
	}
	return files
}
