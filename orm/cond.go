package orm

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/internal/entity"
	"github.com/smatify/tenet/mapping"
)

// Cond is a query condition. Fields are referenced as "Name", "alias.Name"
// or with generated references (PostFields.Title).
type Cond interface {
	build(q *Query) (dbal.Expr, error)
}

type cmpCond struct {
	field any
	op    string
	value any
}

// Eq matches field = value (IS NULL for nil). Owning to-one associations
// compare their foreign key; value may be an entity pointer.
func Eq(field, value any) Cond { return cmpCond{field, "=", value} }

// Neq matches field <> value (IS NOT NULL for nil).
func Neq(field, value any) Cond { return cmpCond{field, "<>", value} }

// Gt matches field > value.
func Gt(field, value any) Cond { return cmpCond{field, ">", value} }

// Gte matches field >= value.
func Gte(field, value any) Cond { return cmpCond{field, ">=", value} }

// Lt matches field < value.
func Lt(field, value any) Cond { return cmpCond{field, "<", value} }

// Lte matches field <= value.
func Lte(field, value any) Cond { return cmpCond{field, "<=", value} }

// Like matches field LIKE pattern.
func Like(field any, pattern string) Cond { return cmpCond{field, "LIKE", pattern} }

// IsNull matches NULL values.
func IsNull(field any) Cond { return cmpCond{field, "=", nil} }

// IsNotNull matches non-NULL values.
func IsNotNull(field any) Cond { return cmpCond{field, "<>", nil} }

func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

// value converts v for comparison with f (entity pointers become their ID).
func (q *Query) value(f *mapping.Field, v any) (any, error) {
	if f.Assoc != nil && entity.Of(v) != nil {
		t := f.Assoc.Target
		ref := t.FieldByColumn(f.Assoc.JoinColumns[0].Referenced)
		if reflect.TypeOf(v) != reflect.PointerTo(t.GoType) {
			return nil, fmt.Errorf("query: %s.%s expects *%s, got %T", f.Assoc.Source.Name, f.Assoc.Name, t.Name, v)
		}
		v = t.Accessor().Get(v, ref.Pos)
	}
	return idArg{f: f, v: v}, nil
}

func (c cmpCond) build(q *Query) (dbal.Expr, error) {
	col, f, err := q.resolve(c.field)
	if err != nil {
		return nil, err
	}
	if isNilValue(c.value) {
		if c.op == "=" {
			return dbal.SQL("? IS NULL", dbal.Ident(col)), nil
		}
		return dbal.SQL("? IS NOT NULL", dbal.Ident(col)), nil
	}
	v, err := q.value(f, c.value)
	if err != nil {
		return nil, err
	}
	return dbal.SQL("? "+c.op+" ?", dbal.Ident(col), v), nil
}

type inCond struct {
	field  any
	values any
	not    bool
}

// In matches field IN (values...); values is a slice. Repeated values are
// bound once. A list that takes more than half the bind parameters of a
// statement is sent as one array parameter on Postgres (field = ANY(?)) and
// may have any length; on other databases a list longer than the limit
// (SQLite 32766, MySQL 65535, SQL Server 2000) fails before it is sent.
func In(field, values any) Cond { return inCond{field: field, values: values} }

// NotIn matches field NOT IN (values...).
func NotIn(field, values any) Cond { return inCond{field: field, values: values, not: true} }

func (c inCond) build(q *Query) (dbal.Expr, error) {
	col, f, err := q.resolve(c.field)
	if err != nil {
		return nil, err
	}
	rv := reflect.ValueOf(c.values)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("query: In expects a slice, got %T", c.values)
	}
	args := make([]any, rv.Len())
	for i := range rv.Len() {
		if args[i], err = q.value(f, rv.Index(i).Interface()); err != nil {
			return nil, err
		}
	}
	return dbal.IsIn(dbal.Ident(col), args, c.not), nil
}

type groupCond struct {
	sep   string
	conds []Cond
}

// And joins conditions with AND.
func And(conds ...Cond) Cond { return groupCond{" AND ", conds} }

// Or joins conditions with OR.
func Or(conds ...Cond) Cond { return groupCond{" OR ", conds} }

func (g groupCond) build(q *Query) (dbal.Expr, error) {
	if len(g.conds) == 0 {
		return dbal.Safe("1 = 1"), nil
	}
	var parts []any
	tmpl := ""
	for i, c := range g.conds {
		e, err := c.build(q)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			tmpl += g.sep
		}
		tmpl += "(?)"
		parts = append(parts, e)
	}
	return dbal.SQL(tmpl, parts...), nil
}

type notCond struct{ c Cond }

// Not negates a condition.
func Not(c Cond) Cond { return notCond{c} }

func (n notCond) build(q *Query) (dbal.Expr, error) {
	e, err := n.c.build(q)
	if err != nil {
		return nil, err
	}
	return dbal.SQL("NOT (?)", e), nil
}

type rawCond struct {
	sql  string
	args []any
}

var fieldPlaceholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\}`)

// Raw is a SQL condition with ? arguments. {Field} or {alias.Field} is
// replaced by the quoted column, e.g. Raw("LOWER({a.Name}) = ?", "ann").
// A slice after IN is expanded: Raw("{ID} IN ?", ids) or "IN (?)"; elsewhere
// (e.g. "= ANY(?)") it is passed as one array parameter.
func Raw(sql string, args ...any) Cond { return rawCond{sql, args} }

var inBefore = regexp.MustCompile(`(?i)\bIN\s*(\(\s*)?$`)

// expandIn wraps slice arguments of "IN ?" and "IN (?)" in dbal.In.
func expandIn(sql string, args []any) (string, []any) {
	var b strings.Builder
	out := slices.Clone(args)
	n, inString := 0, false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case c == '\'':
			inString = !inString
		case c == '?' && !inString && i+1 < len(sql) && sql[i+1] == '?':
			b.WriteString("??")
			i++
			continue
		case c == '?' && !inString:
			if n < len(args) && isSlice(args[n]) {
				if m := inBefore.FindStringSubmatch(b.String()); m != nil {
					out[n] = dbal.In(args[n])
					rest := strings.TrimLeft(sql[i+1:], " \t\n")
					if m[1] != "" && strings.HasPrefix(rest, ")") {
						// "IN (?)": In writes the parentheses.
						kept := strings.TrimSuffix(b.String(), m[1])
						b.Reset()
						b.WriteString(kept)
						b.WriteByte('?')
						i = len(sql) - len(rest)
						n++
						continue
					}
				}
			}
			n++
		}
		b.WriteByte(c)
	}
	return b.String(), out
}

func isSlice(v any) bool {
	if _, ok := v.(driver.Valuer); ok {
		return false
	}
	if _, ok := v.(dbal.Expr); ok {
		return false
	}
	t := reflect.TypeOf(v)
	return t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && t.Elem().Kind() != reflect.Uint8
}

func (r rawCond) build(q *Query) (dbal.Expr, error) {
	var err error
	d := q.em.db.Dialect()
	out := fieldPlaceholder.ReplaceAllStringFunc(r.sql, func(m string) string {
		col, _, e := q.resolve(m[1 : len(m)-1])
		if e != nil {
			err = e
			return m
		}
		w := dbal.NewSQLWriter(d)
		w.WriteIdent(col)
		return w.String()
	})
	if err != nil {
		return nil, err
	}
	out, args := expandIn(out, r.args)
	return dbal.SQL(out, args...), nil
}
