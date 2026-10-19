package orm

import (
	"context"
	"slices"
	"strings"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
)

// joinSpec describes how a to-many association of owner is stored.
type joinSpec struct {
	table    string // target table (oneToMany) or join table (manyToMany)
	ownerCol string // column referencing the owner
	ownerRef *mapping.Field
	// manyToMany only
	targetCol string
	targetRef *mapping.Field
}

func toManySpec(a *mapping.Association) joinSpec {
	owner, target := a.Source, a.Target
	if a.Kind == mapping.OneToMany {
		jc := a.Inverse().JoinColumns[0]
		return joinSpec{table: target.Table, ownerCol: jc.Name, ownerRef: owner.FieldByColumn(jc.Referenced)}
	}
	jt := a.JoinTable
	if !a.Owning {
		jt = a.Inverse().JoinTable
		return joinSpec{
			table: jt.Name, ownerCol: jt.InverseJoinColumns[0].Name, ownerRef: owner.FieldByColumn(jt.InverseJoinColumns[0].Referenced),
			targetCol: jt.JoinColumns[0].Name, targetRef: target.FieldByColumn(jt.JoinColumns[0].Referenced),
		}
	}
	return joinSpec{
		table: jt.Name, ownerCol: jt.JoinColumns[0].Name, ownerRef: owner.FieldByColumn(jt.JoinColumns[0].Referenced),
		targetCol: jt.InverseJoinColumns[0].Name, targetRef: target.FieldByColumn(jt.InverseJoinColumns[0].Referenced),
	}
}

func refArg(m *mapping.Entity, f *mapping.Field, ptr any) idArg {
	return idArg{f: f, v: m.Accessor().Get(ptr, f.Pos)}
}

// loadCollection returns the entities of a to-many association of owner.
func (em *EntityManager) loadCollection(ctx context.Context, owner *entry, a *mapping.Association) ([]any, error) {
	spec := toManySpec(a)
	ownerID := refArg(owner.meta, spec.ownerRef, owner.ptr)
	return em.loadWhere(ctx, a.Target, func(q *dbal.SelectQuery) {
		if a.Kind == mapping.OneToMany {
			q.Where("? = ?", dbal.Ident(spec.ownerCol), ownerID)
		} else {
			sub := em.db.NewSelect().Column(spec.targetCol).Table(spec.table).Where("? = ?", dbal.Ident(spec.ownerCol), ownerID)
			q.Where("? IN ?", dbal.Ident(spec.targetRef.Column), sub)
		}
		applyOrderBy(q, a)
	}, 0)
}

// applyOrderBy translates "Field DESC, Other" into ORDER BY on columns.
func applyOrderBy(q *dbal.SelectQuery, a *mapping.Association) {
	for _, part := range strings.Split(a.OrderBy, ",") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		f := a.Target.Field(fields[0])
		if f == nil {
			f = a.Target.FieldByColumn(fields[0])
		}
		if f == nil {
			continue
		}
		dir := dbal.Asc
		if len(fields) > 1 && strings.EqualFold(fields[1], "desc") {
			dir = dbal.Desc
		}
		q.OrderBy(f.Column, dir)
	}
}

// countCollection counts a to-many association without loading it.
func (em *EntityManager) countCollection(ctx context.Context, owner *entry, a *mapping.Association) (int, error) {
	spec := toManySpec(a)
	return em.db.NewSelect().Table(spec.table).
		Where("? = ?", dbal.Ident(spec.ownerCol), refArg(owner.meta, spec.ownerRef, owner.ptr)).Count(ctx)
}

// wireCollections attaches fresh, uninitialized collection states to a
// hydrated entity and resets Ref containers handled by setAssoc.
func (em *EntityManager) wireCollections(e *entry) {
	acc := e.meta.Accessor()
	for _, a := range e.meta.Assocs {
		if a.Container != mapping.ContainerCollection {
			continue
		}
		c := acc.AssocAddr(e.ptr, a.Pos).(collection)
		c.setState(&collState{owner: e, assoc: a})
	}
}

// adoptCollections links the collections of a newly inserted entity to it.
func (em *EntityManager) adoptCollections(e *entry) {
	acc := e.meta.Accessor()
	for _, a := range e.meta.Assocs {
		if a.Container != mapping.ContainerCollection {
			continue
		}
		c := acc.AssocAddr(e.ptr, a.Pos).(collection)
		if st := c.state(); st != nil && st.owner == e {
			continue
		}
		c.setState(&collState{initialized: true, owner: e, assoc: a, snapshot: c.Entities()})
	}
}

// scheduleOrphans removes entities that were taken out of orphanRemoval
// collections or replaced in owning orphanRemoval oneToOne associations.
func (em *EntityManager) scheduleOrphans() error {
	for i := 0; i < len(em.tracked); i++ {
		e := em.tracked[i]
		if e.state != StateManaged || !e.initialized {
			continue
		}
		acc := e.meta.Accessor()
		for _, a := range e.meta.Assocs {
			if !a.OrphanRemoval {
				continue
			}
			switch {
			case a.Container == mapping.ContainerCollection:
				c := acc.AssocAddr(e.ptr, a.Pos).(collection)
				st := c.state()
				if st == nil {
					continue
				}
				current := c.Entities()
				for _, r := range st.removed {
					if !slices.Contains(current, r) {
						if err := em.remove(a.Target, r, map[any]bool{}); err != nil {
							return err
						}
					}
				}
				st.removed = nil
			case a.Owning && a.Kind == mapping.OneToOne:
				f := e.meta.FieldByColumn(a.JoinColumns[0].Name)
				old := e.snapshot[f.Pos]
				now, err := em.fieldDBValue(e, f)
				if err != nil {
					return err
				}
				if old == nil || equalDB(old, now) {
					continue
				}
				for _, cand := range em.identity[a.Target] {
					tf := a.Target.FieldByColumn(a.JoinColumns[0].Referenced)
					v, err := tf.Type.ToDatabase(a.Target.Accessor().Get(cand.ptr, tf.Pos), em.db.Dialect().Name())
					if err == nil && equalDB(v, old) && cand.state == StateManaged {
						cand.state = StateRemoved
					}
				}
			}
		}
	}
	return nil
}

type joinRow struct {
	spec   joinSpec
	owner  *entry
	target any
	insert bool
}

// joinTableChanges computes inserts and deletes for owning manyToMany
// collections.
func (em *EntityManager) joinTableChanges() []joinRow {
	var out []joinRow
	for _, e := range em.tracked {
		if (e.state != StateManaged && e.state != StateNew) || !e.initialized {
			continue
		}
		acc := e.meta.Accessor()
		for _, a := range e.meta.Assocs {
			if a.Kind != mapping.ManyToMany || !a.Owning || a.Container != mapping.ContainerCollection {
				continue
			}
			c := acc.AssocAddr(e.ptr, a.Pos).(collection)
			var prev []any
			if st := c.state(); st != nil && st.owner == e {
				if !st.initialized && len(st.added) == 0 {
					continue
				}
				prev = st.snapshot
			}
			current := c.Entities()
			spec := toManySpec(a)
			for _, t := range current {
				if !slices.Contains(prev, t) {
					out = append(out, joinRow{spec: spec, owner: e, target: t, insert: true})
				}
			}
			if st := c.state(); st != nil && st.initialized {
				for _, t := range prev {
					if !slices.Contains(current, t) {
						out = append(out, joinRow{spec: spec, owner: e, target: t})
					}
				}
			}
		}
	}
	return out
}

func (em *EntityManager) writeJoinRow(ctx context.Context, db dbal.IDB, r joinRow, target *mapping.Entity) error {
	ownerID := refArg(r.owner.meta, r.spec.ownerRef, r.owner.ptr)
	targetID := refArg(target, r.spec.targetRef, r.target)
	if r.insert {
		_, err := db.NewInsert().Table(r.spec.table).Columns(r.spec.ownerCol, r.spec.targetCol).Values(ownerID, targetID).Exec(ctx)
		return err
	}
	_, err := db.NewDelete().Table(r.spec.table).
		Where("? = ?", dbal.Ident(r.spec.ownerCol), ownerID).
		Where("? = ?", dbal.Ident(r.spec.targetCol), targetID).Exec(ctx)
	return err
}

// syncCollectionSnapshots records the flushed state of every collection.
func (em *EntityManager) syncCollectionSnapshots() {
	for _, e := range em.tracked {
		if e.state != StateManaged || !e.initialized {
			continue
		}
		acc := e.meta.Accessor()
		for _, a := range e.meta.Assocs {
			if a.Container != mapping.ContainerCollection {
				continue
			}
			c := acc.AssocAddr(e.ptr, a.Pos).(collection)
			if st := c.state(); st != nil && st.initialized {
				st.snapshot = c.Entities()
			}
		}
	}
}
