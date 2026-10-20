package orm

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
)

// ResultSetMapping describes how the columns of a native SQL result map to
// an entity, like Doctrine's ResultSetMapping. By default result columns
// match mapped column names (case-insensitive); Rename maps differently
// named result columns.
type ResultSetMapping struct {
	// Rename maps result column -> mapped column (or "discriminator" for
	// the discriminator of an inheritance hierarchy).
	Rename map[string]string
}

// NativeQuery runs sql (with ? placeholders) and hydrates entities of type t
// from its rows. The result must contain every mapped column of each
// row's class; extra columns are ignored. Rows of classes with subclasses
// need the discriminator column. Results go through the identity map.
func (em *EntityManager) NativeQuery(ctx context.Context, t reflect.Type, rsm *ResultSetMapping, sql string, args ...any) ([]any, error) {
	if em.closed {
		return nil, ErrClosed
	}
	meta, err := em.reg.Get(t)
	if err != nil {
		return nil, err
	}
	q, qargs, err := dbal.Build(em.db.Dialect(), dbal.SQL(sql, args...))
	if err != nil {
		return nil, err
	}
	rows, err := em.db.QueryContext(ctx, q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	// position of each mapped column (lower case) in the result
	pos := map[string]int{}
	for i, c := range cols {
		name := strings.ToLower(c)
		if rsm != nil {
			for from, to := range rsm.Rename {
				if strings.EqualFold(from, c) {
					name = strings.ToLower(to)
				}
			}
		}
		pos[name] = i
	}
	discPos := -1
	if meta.Inheritance != "" {
		if i, ok := pos["discriminator"]; ok {
			discPos = i
		} else if i, ok := pos[strings.ToLower(meta.DiscriminatorColumn)]; ok {
			discPos = i
		} else if meta.IsPolymorphic() {
			return nil, fmt.Errorf("native query for %s must return the discriminator column %q", meta.Name, meta.DiscriminatorColumn)
		}
	}
	type rowData struct {
		class *mapping.Entity
		vals  []any
	}
	var data []rowData
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		class := meta
		if discPos >= 0 {
			v := fmt.Sprint(normalizeText(raw[discPos]))
			if class = meta.ClassForDiscriminator(v); class == nil || !isA(class, meta) {
				return nil, fmt.Errorf("native query: unknown discriminator value %q for %s", v, meta.Name)
			}
		}
		vals := make([]any, len(class.Fields))
		var missing []string
		for i, f := range class.Fields {
			p, ok := pos[strings.ToLower(f.Column)]
			if !ok {
				missing = append(missing, f.Column)
				continue
			}
			v := raw[p]
			if b, ok := v.([]byte); ok {
				v = slices.Clone(b)
			}
			vals[i] = v
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("native query for %s is missing columns %s", class.Name, strings.Join(missing, ", "))
		}
		data = append(data, rowData{class, vals})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	out := make([]any, 0, len(data))
	for _, d := range data {
		h := em.newHydrator(d.class)
		ptr, err := h.values(ctx, d.vals, nil)
		if err != nil {
			return nil, err
		}
		if err := em.resolvePending(ctx, h.pending); err != nil {
			return nil, err
		}
		if e, ok := entryFromPtr(ptr); ok {
			em.cachePut(e)
			ptr = view(e, meta)
		}
		out = append(out, ptr)
	}
	if len(out) > 0 {
		if err := em.preloadEager(ctx, meta, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// NativeScalars runs sql and returns each row as a column map (byte slices
// from text columns are returned as strings).
func (em *EntityManager) NativeScalars(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	q, qargs, err := dbal.Build(em.db.Dialect(), dbal.SQL(sql, args...))
	if err != nil {
		return nil, err
	}
	rows, err := em.db.QueryContext(ctx, q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = normalizeText(raw[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func normalizeText(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
