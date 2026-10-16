// Package diag defines Loom's diagnostics: compile errors with a stable
// code, a position, a code excerpt and a fix.
//
// Every error a user (or an agent) sees should say what is wrong, where,
// and how to fix it. Each kind of error has a stable code, listed in
// codes.go and documented in docs/errors.md; a test keeps the two in sync.
package diag

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/moomerman/loom/compiler/source"
)

// Error is a compile error at a position in a file. It formats as:
//
//	todo.hx:4:3: error LX203: <li> is never closed
//	  |
//	4 |   <li :for={t <- todos}>
//	  |   ^^^
//	  = fix: add </li> where the element should end
type Error struct {
	Code Code
	File *source.File
	Pos  source.Pos
	End  int // byte offset where the underlined span ends; <= Pos.Offset underlines one character
	Msg  string
	Fix  string
	// Warning marks a diagnostic that doesn't stop the build, such as a
	// bare absolute URL in a package's templates (TC336).
	Warning bool
}

// Severity is "error" or "warning".
func (e *Error) Severity() string {
	if e.Warning {
		return "warning"
	}
	return "error"
}

// Errorf builds an Error with the given code spanning [start, end) in f.
func Errorf(code Code, f *source.File, start, end int, format string, args ...any) *Error {
	return &Error{Code: code, File: f, Pos: f.Pos(start), End: end, Msg: fmt.Sprintf(format, args...)}
}

// Warnf builds a warning with the given code spanning [start, end) in f.
func Warnf(code Code, f *source.File, start, end int, format string, args ...any) *Error {
	e := Errorf(code, f, start, end, format, args...)
	e.Warning = true
	return e
}

// WithFix sets how to fix the error and returns the error.
func (e *Error) WithFix(format string, args ...any) *Error {
	e.Fix = fmt.Sprintf(format, args...)
	return e
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d:%d: %s %s: %s\n", e.File.Name, e.Pos.Line, e.Pos.Col, e.Severity(), e.Code, e.Msg)

	line := e.File.Line(e.Pos.Line)
	num := fmt.Sprint(e.Pos.Line)
	gutter := strings.Repeat(" ", len(num))
	fmt.Fprintf(&b, "%s |\n", gutter)
	fmt.Fprintf(&b, "%s | %s\n", num, line)

	// Build the caret line, copying tabs so the carets line up.
	var caret strings.Builder
	col := 1
	for _, r := range line {
		if col >= e.Pos.Col {
			break
		}
		if r == '\t' {
			caret.WriteByte('\t')
		} else {
			caret.WriteByte(' ')
		}
		col++
	}
	width := 1
	if e.End > e.Pos.Offset {
		lineEnd := e.File.LineStart(e.Pos.Line) + len(line)
		end := min(e.End, lineEnd) // underline at most to the end of the line
		if end > e.Pos.Offset {
			width = max(1, utf8.RuneCountInString(e.File.Text[e.Pos.Offset:end]))
		}
	}
	fmt.Fprintf(&b, "%s | %s%s\n", gutter, caret.String(), strings.Repeat("^", width))
	if e.Fix != "" {
		fmt.Fprintf(&b, "%s = fix: %s\n", gutter, e.Fix)
	}
	return b.String()
}

// Diagnostic is the JSON form of an error, as printed by `loom check --json`.
// Its schema is documented in docs/cli.md and must stay stable.
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}

// Diagnostic returns the JSON form of the error.
func (e *Error) Diagnostic() Diagnostic {
	return Diagnostic{
		Code:     string(e.Code),
		Severity: e.Severity(),
		File:     e.File.Name,
		Line:     e.Pos.Line,
		Col:      e.Pos.Col,
		Message:  e.Msg,
		Fix:      e.Fix,
	}
}

// MarshalJSON encodes the error as a Diagnostic.
func (e *Error) MarshalJSON() ([]byte, error) { return json.Marshal(e.Diagnostic()) }

// List is a list of errors, reported together.
type List []*Error

func (l List) Error() string {
	parts := make([]string, len(l))
	for i, e := range l {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

// HasErrors reports whether the list holds an error, not only warnings.
func (l List) HasErrors() bool {
	for _, e := range l {
		if !e.Warning {
			return true
		}
	}
	return false
}

// Err returns nil for an empty list, so callers can `return errs.Err()`.
func (l List) Err() error {
	if len(l) == 0 {
		return nil
	}
	return l
}

// Errors flattens err into a list of *Error. Errors of other types are
// not compile errors and yield nil.
func Errors(err error) List {
	switch err := err.(type) {
	case *Error:
		return List{err}
	case List:
		return err
	}
	return nil
}
