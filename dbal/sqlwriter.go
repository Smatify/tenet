package dbal

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/smatify/tenet/dialect"
)

// Expr is anything that can render itself as SQL.
type Expr interface {
	AppendSQL(w *SQLWriter) error
}

// SQLWriter accumulates SQL text and bind arguments for a dialect.
type SQLWriter struct {
	d    dialect.Dialect
	buf  strings.Builder
	args []any
}

// NewSQLWriter returns a writer for d.
func NewSQLWriter(d dialect.Dialect) *SQLWriter { return &SQLWriter{d: d} }

// Dialect returns the target dialect.
func (w *SQLWriter) Dialect() dialect.Dialect { return w.d }

// String returns the SQL written so far.
func (w *SQLWriter) String() string { return w.buf.String() }

// Args returns the collected bind arguments.
func (w *SQLWriter) Args() []any { return w.args }

// WriteString appends raw SQL.
func (w *SQLWriter) WriteString(s string) { w.buf.WriteString(s) }

// WriteByte appends a single raw byte.
func (w *SQLWriter) WriteByte(c byte) error { return w.buf.WriteByte(c) }

// WriteIdent appends a quoted identifier. Dots separate qualifiers
// ("u.name" -> "u"."name") and a trailing "*" is kept unquoted.
func (w *SQLWriter) WriteIdent(name string) {
	for i := 0; ; i++ {
		part, rest, more := strings.Cut(name, ".")
		if i > 0 {
			w.buf.WriteByte('.')
		}
		if part == "*" {
			w.buf.WriteByte('*')
		} else {
			w.buf.WriteString(quoteIdent(w.d, part))
		}
		if !more {
			return
		}
		name = rest
	}
}

// quoted caches quoted identifiers per dialect; identifiers come from the
// mapping and queries, so the set is small. It stops growing at maxQuoted
// entries in case an application builds identifiers dynamically.
var (
	quotedMu sync.RWMutex
	quoted   = map[quoteKey]string{}
)

const maxQuoted = 8192

type quoteKey struct {
	d     dialect.Dialect
	ident string
}

func quoteIdent(d dialect.Dialect, ident string) string {
	if !reflect.TypeOf(d).Comparable() {
		return d.QuoteIdent(ident)
	}
	k := quoteKey{d, ident}
	quotedMu.RLock()
	q, ok := quoted[k]
	quotedMu.RUnlock()
	if ok {
		return q
	}
	q = d.QuoteIdent(ident)
	quotedMu.Lock()
	if len(quoted) < maxQuoted {
		quoted[k] = q
	}
	quotedMu.Unlock()
	return q
}

// WriteArg appends a bind placeholder for v. Expr values are rendered inline.
func (w *SQLWriter) WriteArg(v any) error {
	if e, ok := v.(Expr); ok {
		return e.AppendSQL(w)
	}
	w.args = append(w.args, v)
	w.buf.WriteString(w.d.Placeholder(len(w.args)))
	return nil
}

// ErrArgCount is returned when the number of ? placeholders and arguments differ.
var ErrArgCount = errors.New("dbal: placeholder/argument count mismatch")

// WriteExpr appends query, replacing each ? with the next argument. "??"
// produces a literal question mark. Question marks inside single-quoted
// string literals are left alone.
func (w *SQLWriter) WriteExpr(query string, args ...any) error {
	n := 0
	inString := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			inString = !inString
			w.buf.WriteByte(c)
		case c == '?' && !inString:
			if i+1 < len(query) && query[i+1] == '?' {
				w.buf.WriteByte('?')
				i++
				continue
			}
			if n >= len(args) {
				return fmt.Errorf("%w in %q", ErrArgCount, query)
			}
			if err := w.WriteArg(args[n]); err != nil {
				return err
			}
			n++
		default:
			w.buf.WriteByte(c)
		}
	}
	if n != len(args) {
		return fmt.Errorf("%w in %q: %d placeholders, %d args", ErrArgCount, query, n, len(args))
	}
	return nil
}

// Ident is an identifier argument that is quoted instead of bound.
type Ident string

// AppendSQL implements Expr.
func (i Ident) AppendSQL(w *SQLWriter) error { w.WriteIdent(string(i)); return nil }

// Safe is trusted raw SQL inserted verbatim. Never pass user input.
type Safe string

// AppendSQL implements Expr.
func (s Safe) AppendSQL(w *SQLWriter) error { w.WriteString(string(s)); return nil }

// RawExpr is a SQL fragment with ? placeholders.
type RawExpr struct {
	Query string
	Args  []any
}

// SQL returns a fragment with ? placeholders.
func SQL(query string, args ...any) RawExpr { return RawExpr{Query: query, Args: args} }

// AppendSQL implements Expr.
func (e RawExpr) AppendSQL(w *SQLWriter) error { return w.WriteExpr(e.Query, e.Args...) }

// InExpr expands a slice into a parenthesized placeholder list.
type InExpr struct{ values any }

// In expands a slice into (?, ?, ...). An empty slice renders (NULL) so that
// "x IN ?" matches nothing instead of producing invalid SQL. Values that
// repeat are bound once: lists collected from many rows (the game of every
// match) stay within the bind parameter limit of the database.
func In(slice any) InExpr { return InExpr{values: slice} }

// AppendSQL implements Expr.
func (e InExpr) AppendSQL(w *SQLWriter) error {
	rv := reflect.ValueOf(e.values)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return fmt.Errorf("dbal: In expects a slice, got %T", e.values)
	}
	w.WriteString("(")
	if rv.Len() == 0 {
		w.WriteString("NULL")
	}
	seen := map[any]bool{}
	n := 0
	for i := range rv.Len() {
		v := rv.Index(i).Interface()
		if v != nil && reflect.ValueOf(v).Comparable() {
			if seen[v] {
				continue
			}
			seen[v] = true
		}
		if n > 0 {
			w.WriteString(", ")
		}
		n++
		if err := w.WriteArg(v); err != nil {
			return err
		}
	}
	w.WriteString(")")
	return nil
}

// Build renders e and returns the SQL with its arguments.
func Build(d dialect.Dialect, e Expr) (string, []any, error) {
	w := NewSQLWriter(d)
	if err := e.AppendSQL(w); err != nil {
		return "", nil, err
	}
	return w.String(), w.Args(), nil
}

func writeIdents(w *SQLWriter, names []string) {
	for i, n := range names {
		if i > 0 {
			w.WriteString(", ")
		}
		w.WriteIdent(n)
	}
}

func writeExprs(w *SQLWriter, exprs []Expr) error {
	for i, e := range exprs {
		if i > 0 {
			w.WriteString(", ")
		}
		if err := e.AppendSQL(w); err != nil {
			return err
		}
	}
	return nil
}
