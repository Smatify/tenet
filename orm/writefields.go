package orm

import (
	"context"
	"fmt"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/mapping"
)

// WriteFields writes the current values of the named fields of a managed
// entity at once: without the optimistic version check and increment of a
// flush and without the other pending changes of the entity manager. It is
// meant for bookkeeping that must not count as an edit, e.g. edit locks or
// view counters. The written values become part of the snapshot, so the
// next flush does not write them again.
func (em *EntityManager) WriteFields(ctx context.Context, ptr any, names ...string) error {
	if em.closed {
		return ErrClosed
	}
	meta, err := em.meta(ptr)
	if err != nil {
		return err
	}
	e := em.entryOf(meta, ptr)
	if e == nil || e.state != StateManaged || !e.initialized {
		return fmt.Errorf("%s: WriteFields requires a managed, loaded entity", meta.Name)
	}
	fields := make([]*mapping.Field, 0, len(names))
	for _, n := range names {
		f := meta.Field(n)
		if f == nil {
			if a := meta.Assoc(n); a != nil && a.Owning && a.Kind.IsToOne() {
				f = meta.FieldByColumn(a.JoinColumns[0].Name)
			}
		}
		switch {
		case f == nil:
			return fmt.Errorf("%s: WriteFields: unknown field %q", meta.Name, n)
		case f.Version || f.ID:
			return fmt.Errorf("%s: WriteFields cannot write the identifier or version %q", meta.Name, n)
		}
		fields = append(fields, f)
	}
	var db dbal.IDB = em.db
	if err := em.update(ctx, db, e, fields); err != nil {
		return err
	}
	if e.snapshot != nil {
		for _, f := range fields {
			if v, err := em.fieldDBValue(e, f); err == nil {
				e.snapshot[f.Pos] = v
			}
		}
	}
	return nil
}
