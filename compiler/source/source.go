// Package source holds template source files and positions within them.
package source

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// File is a template source file.
type File struct {
	Name  string
	Text  string
	lines []int // byte offset of the start of each line
}

// NewFile returns a File for the given name and contents.
func NewFile(name, text string) *File {
	f := &File{Name: name, Text: text, lines: []int{0}}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			f.lines = append(f.lines, i+1)
		}
	}
	return f
}

// Pos is a position in a File. Line and Col are 1-based; Col counts
// characters (runes), not bytes.
type Pos struct {
	Offset int
	Line   int
	Col    int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Pos converts a byte offset into a Pos.
func (f *File) Pos(offset int) Pos {
	if offset > len(f.Text) {
		offset = len(f.Text)
	}
	line := sort.Search(len(f.lines), func(i int) bool { return f.lines[i] > offset }) - 1
	col := utf8.RuneCountInString(f.Text[f.lines[line]:offset]) + 1
	return Pos{Offset: offset, Line: line + 1, Col: col}
}

// Line returns the text of the 1-based line n, without its newline.
func (f *File) Line(n int) string {
	if n < 1 || n > len(f.lines) {
		return ""
	}
	start := f.lines[n-1]
	end := len(f.Text)
	if n < len(f.lines) {
		end = f.lines[n] - 1
	}
	return strings.TrimSuffix(f.Text[start:end], "\r")
}

// LineStart returns the byte offset where the 1-based line n starts.
func (f *File) LineStart(n int) int { return f.lines[n-1] }
