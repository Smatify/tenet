package smorm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// Scan runs a query with Columns and reads its rows into R: a struct
// whose exported fields take the columns of the same name (the `db` tag,
// or the field name in snake case: LastAt takes last_at), or a single
// value for a query of one column, e.g.
//
//	type count struct {
//		ID uuid.UUID
//		N  int
//	}
//	counts, err := smorm.Scan[count](ctx, smorm.NewQuery[Comment](em).Columns("{ObjectID} AS id", "count(*) AS n").GroupBy("{ObjectID}"))
func Scan[R any](ctx context.Context, q Selection) ([]R, error) {
	rows, err := q.ormQuery().Rows(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	t := reflect.TypeFor[R]()
	var fields []int // field of each column; nil for a single value
	if t.Kind() == reflect.Struct && t != reflect.TypeFor[time.Time]() && !reflect.PointerTo(t).Implements(reflect.TypeFor[sql.Scanner]()) {
		byName := map[string]int{}
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := f.Tag.Get("db")
			if name == "" {
				name = snake(f.Name)
			}
			byName[name] = i
		}
		for _, c := range cols {
			i, ok := byName[strings.ToLower(c)]
			if !ok {
				return nil, fmt.Errorf("scan: %s has no field for column %q", t, c)
			}
			fields = append(fields, i)
		}
	} else if len(cols) != 1 {
		return nil, fmt.Errorf("scan: %d columns into %s", len(cols), t)
	}
	var out []R
	dest := make([]any, len(cols))
	for rows.Next() {
		var r R
		v := reflect.ValueOf(&r).Elem()
		if fields == nil {
			dest[0] = &r
		} else {
			for i, f := range fields {
				dest[i] = v.Field(f).Addr().Interface()
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// snake turns a Go name into snake case: LastAt → last_at, ID → id,
// ThreadID → thread_id.
func snake(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1])) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
