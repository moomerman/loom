package data

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// MapError turns an error the database reported into an *Error with a
// code and a fix, where Loom knows one: unique, foreign key and not-null
// violations, missing tables and columns, syntax errors, and type
// mismatches. Other errors are returned unchanged.
// The driver's error stays reachable with errors.As. Querier methods
// already map their errors; call MapError on errors from the *sql.DB
// that db.SQL() returns.
func MapError(err error) error {
	var pg *pgconn.PgError
	if err == nil || !errors.As(err, &pg) {
		return err
	}
	var e *Error
	if errors.As(err, &e) {
		return err // already mapped
	}
	detail := ""
	if pg.Detail != "" {
		detail = ": " + pg.Detail
	}
	switch pg.Code {
	case "23505":
		return &Error{Code: DB606, Err: err,
			Msg: fmt.Sprintf("a row in %s already has this value of unique constraint %q%s", pg.TableName, pg.ConstraintName, detail),
			Fix: "check for the existing row first, or write INSERT ... ON CONFLICT DO NOTHING (or DO UPDATE); detect this case with errors.Is(err, loom.ErrUniqueViolation)"}
	case "23503":
		return &Error{Code: DB607, Err: err,
			Msg: fmt.Sprintf("this change to %s breaks foreign key %q%s", pg.TableName, pg.ConstraintName, detail),
			Fix: "insert the referenced row first, or delete the rows that reference this one first (or declare the foreign key ON DELETE CASCADE); detect this case with errors.Is(err, loom.ErrForeignKeyViolation)"}
	case "23502":
		return &Error{Code: DB608, Err: err,
			Msg: fmt.Sprintf("column %q of %s can't be null, but the statement gives it no value or NULL", pg.ColumnName, pg.TableName),
			Fix: fmt.Sprintf("give %s a value in the statement, or give the column a default in a migration", pg.ColumnName)}
	case "42P01":
		return &Error{Code: DB609, Err: err,
			Msg: pg.Message,
			Fix: "check the table name; if it is created by a migration that hasn't run, run `loom migrate` (`loom migrate status` shows what is pending)"}
	case "42703":
		return &Error{Code: DB618, Err: err,
			Msg: pg.Message,
			Fix: hinted(pg, "check the name against the table's columns (`loom db schema`); if a migration adds the column and hasn't run, run `loom migrate`")}
	case "42601":
		return &Error{Code: DB619, Err: err,
			Msg: pg.Message,
			Fix: hinted(pg, "fix the SQL where it points; parameters are $1, $2, ... (not ?), and identifiers that are keywords need double quotes")}
	case "42804":
		return &Error{Code: DB620, Err: err,
			Msg: pg.Message,
			Fix: hinted(pg, "pass a value of the column's type (`loom db schema` shows it), or cast it in the SQL, e.g. $1::bigint")}
	case "42883":
		return &Error{Code: DB621, Err: err,
			Msg: pg.Message,
			Fix: hinted(pg, "cast an argument to the type the function or operator takes, e.g. $1::text; a Go value whose type Postgres can't infer needs a cast")}
	}
	return err
}

// hinted puts Postgres's own hint, such as "Perhaps you meant to
// reference the column "todos.title".", before a fix.
func hinted(pg *pgconn.PgError, fix string) string {
	if pg.Hint == "" {
		return fix
	}
	return strings.TrimSuffix(pg.Hint, ".") + "; otherwise " + fix
}

// mapQueryError maps err like MapError and, when Postgres says where in
// query the problem is, adds an excerpt of the query pointing at it.
func mapQueryError(err error, query string) error {
	err = MapError(err)
	var e *Error
	var pg *pgconn.PgError
	if !errors.As(err, &e) || !errors.As(err, &pg) || pg.Position <= 0 || e.Msg != pg.Message {
		return err
	}
	if ex := excerpt(query, int(pg.Position)); ex != "" {
		e.Msg += "\n" + ex
	}
	return err
}

// excerpt returns the line of query holding the 1-based character
// position pos, with a caret under it.
func excerpt(query string, pos int) string {
	runes := []rune(query)
	if pos < 1 || pos > len(runes)+1 {
		return ""
	}
	start, end := pos-1, pos-1
	for start > 0 && runes[start-1] != '\n' {
		start--
	}
	for end < len(runes) && runes[end] != '\n' {
		end++
	}
	line := string(runes[start:end])
	trimmed := strings.TrimLeft(line, " \t")
	col := pos - 1 - start - (len([]rune(line)) - len([]rune(trimmed)))
	if col < 0 {
		col = 0
	}
	return "    " + trimmed + "\n    " + strings.Repeat(" ", col) + "^"
}
