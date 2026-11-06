package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
)

// preloadChunk limits the number of identifiers per IN list.
const preloadChunk = 500

// Preload loads the given association paths ("Posts", "Posts.Comments",
// "Author") for all roots with one query per association and level,
// avoiding N+1 queries. roots must be entities of the same type.
func (em *EntityManager) Preload(ctx context.Context, roots []any, paths ...string) error {
	if em.closed {
		return ErrClosed
	}
	if len(roots) == 0 {
		return nil
	}
	meta, err := em.meta(roots[0])
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := em.preloadPath(ctx, meta, roots, strings.Split(p, ".")); err != nil {
			return err
		}
	}
	return nil
}

func (em *EntityManager) preloadPath(ctx context.Context, meta *mapping.Entity, owners []any, path []string) error {
	if len(path) == 0 || len(owners) == 0 {
		return nil
	}
	a := meta.Assoc(path[0])
	if a == nil {
		return fmt.Errorf("%s: unknown association %q in preload path", meta.Name, path[0])
	}
	next, err := em.preloadAssoc(ctx, a, owners)
	if err != nil {
		return err
	}
	return em.preloadPath(ctx, a.Target, next, path[1:])
}

// preloadAssoc loads association a for all owners and returns the targets.
func (em *EntityManager) preloadAssoc(ctx context.Context, a *mapping.Association, owners []any) ([]any, error) {
	if a.Kind.IsToOne() {
		if !a.Owning {
			return em.preloadInverseOne(ctx, a, owners)
		}
		var targets []any
		seen := map[any]bool{}
		for _, o := range owners {
			if t := relatedOne(a.Source, o, a); t != nil && !seen[t] {
				seen[t] = true
				targets = append(targets, t)
			}
		}
		return targets, em.resolve(func() error { return em.initializeAll(ctx, a.Target, targets) })
	}
	return em.preloadMany(ctx, a, owners)
}

// initializeAll loads every uninitialized reference in ptrs.
func (em *EntityManager) initializeAll(ctx context.Context, meta *mapping.Entity, ptrs []any) error {
	idf := meta.SingleID()
	var pending []any
	for _, p := range ptrs {
		if e := em.entryOf(meta, p); e != nil && !e.initialized {
			if idf == nil {
				if err := em.refresh(ctx, e); err != nil {
					return err
				}
				continue
			}
			id := e.key // the normalized identifier, already boxed
			if id == nil {
				id = meta.Accessor().Get(p, idf.Pos)
			}
			if pending == nil {
				pending = make([]any, 0, len(ptrs))
			}
			pending = append(pending, idArg{f: idf, v: id})
		}
	}
	for len(pending) > 0 {
		n := min(len(pending), preloadChunk)
		chunk := pending[:n]
		pending = pending[n:]
		if _, err := em.loadWhere(ctx, meta, func(q *dbal.SelectQuery) {
			q.Where("? IN ?", dbal.Ident(idf.Column), dbal.In(chunk))
		}, 0); err != nil {
			return err
		}
	}
	return nil
}

// preloadMany fills the to-many association a of owners.
func (em *EntityManager) preloadMany(ctx context.Context, a *mapping.Association, owners []any) ([]any, error) {
	spec := toManySpec(a)
	// Owners whose association still needs loading, keyed by referenced value.
	byKey := map[any][]any{}
	var keys []any
	for _, o := range owners {
		if c, ok := em.assocCollection(a, o); ok && c.IsInitialized() && c.state() != nil {
			continue
		}
		v := a.Source.Accessor().Get(o, spec.ownerRef.Pos)
		k := normalizeKey(v)
		if _, ok := byKey[k]; !ok {
			keys = append(keys, idArg{f: spec.ownerRef, v: v})
		}
		byKey[k] = append(byKey[k], o)
	}
	children := map[any][]any{} // owner key -> targets in query order
	var all []any
	for len(keys) > 0 {
		n := min(len(keys), preloadChunk)
		chunk := keys[:n]
		keys = keys[n:]
		if a.Kind == mapping.OneToMany {
			inv := a.Inverse()
			res, err := em.loadWhere(ctx, a.Target, func(q *dbal.SelectQuery) {
				q.Where("? IN ?", dbal.Ident(spec.ownerCol), dbal.In(chunk))
				applyOrderBy(q, a)
			}, 0)
			if err != nil {
				return nil, err
			}
			for _, child := range res {
				parent := related(a.Target, child, inv)
				if len(parent) == 0 {
					continue
				}
				k := normalizeKey(a.Source.Accessor().Get(parent[0], spec.ownerRef.Pos))
				children[k] = append(children[k], child)
				all = append(all, child)
			}
			continue
		}
		pairs, err := em.joinPairs(ctx, spec, chunk)
		if err != nil {
			return nil, err
		}
		var targetIDs []any
		seen := map[any]bool{}
		for _, p := range pairs {
			if !seen[p[1]] {
				seen[p[1]] = true
				targetIDs = append(targetIDs, idArg{f: spec.targetRef, v: p[1]})
			}
		}
		var targets []any
		for len(targetIDs) > 0 {
			n := min(len(targetIDs), preloadChunk)
			part := targetIDs[:n]
			targetIDs = targetIDs[n:]
			res, err := em.loadWhere(ctx, a.Target, func(q *dbal.SelectQuery) {
				q.Where("? IN ?", dbal.Ident(spec.targetRef.Column), dbal.In(part))
				applyOrderBy(q, a)
			}, 0)
			if err != nil {
				return nil, err
			}
			targets = append(targets, res...)
		}
		byTarget := map[any]any{}
		for _, t := range targets {
			byTarget[normalizeKey(a.Target.Accessor().Get(t, spec.targetRef.Pos))] = t
		}
		for _, t := range targets { // keep target order
			tk := normalizeKey(a.Target.Accessor().Get(t, spec.targetRef.Pos))
			for _, p := range pairs {
				if normalizeKey(p[1]) == tk {
					k := normalizeKey(p[0])
					children[k] = append(children[k], byTarget[tk])
				}
			}
			all = append(all, t)
		}
	}
	for k, os := range byKey {
		for _, o := range os {
			if err := em.setToMany(a, o, children[k]); err != nil {
				return nil, err
			}
		}
	}
	return all, nil
}

// joinPairs reads (owner, target) identifier pairs from a join table.
func (em *EntityManager) joinPairs(ctx context.Context, spec joinSpec, owners []any) ([][2]any, error) {
	var out [][2]any
	d := em.db.Dialect().Name()
	err := em.db.NewSelect().Column(spec.ownerCol, spec.targetCol).Table(spec.table).
		Where("? IN ?", dbal.Ident(spec.ownerCol), dbal.In(owners)).
		ForEach(ctx, func(rows *sql.Rows) error {
			o := reflect.New(spec.ownerRef.GoType)
			t := reflect.New(spec.targetRef.GoType)
			if err := rows.Scan(spec.ownerRef.Type.ScanTarget(o.Interface(), d), spec.targetRef.Type.ScanTarget(t.Interface(), d)); err != nil {
				return err
			}
			out = append(out, [2]any{o.Elem().Interface(), t.Elem().Interface()})
			return nil
		})
	return out, err
}

func (em *EntityManager) assocCollection(a *mapping.Association, owner any) (collection, bool) {
	if a.Container != mapping.ContainerCollection {
		return nil, false
	}
	return a.Source.Accessor().AssocAddr(owner, a.Pos).(collection), true
}

// setToMany stores loaded targets in the owner's association field.
func (em *EntityManager) setToMany(a *mapping.Association, owner any, targets []any) error {
	switch a.Container {
	case mapping.ContainerCollection:
		c := a.Source.Accessor().AssocAddr(owner, a.Pos).(collection)
		e := em.entryOf(a.Source, owner)
		c.fill(targets)
		c.setState(&collState{initialized: true, owner: e, assoc: a, snapshot: append([]any(nil), targets...)})
	case mapping.ContainerSlice:
		field := reflect.ValueOf(a.Source.Accessor().AssocAddr(owner, a.Pos)).Elem()
		s := reflect.MakeSlice(field.Type(), 0, len(targets))
		for _, t := range targets {
			s = reflect.Append(s, reflect.ValueOf(t))
		}
		field.Set(s)
	}
	return nil
}

// preloadEager loads associations mapped with fetch:eager for freshly
// loaded entities (one level; nested eager associations recurse through
// their own loads).
func (em *EntityManager) preloadEager(ctx context.Context, meta *mapping.Entity, loaded []any) error {
	for _, a := range meta.Assocs {
		// The inverse side of oneToOne cannot be a proxy (its key lives in
		// the other table), so it is always loaded, like in Doctrine.
		inverseOne := a.Kind == mapping.OneToOne && !a.Owning
		if a.Fetch != mapping.FetchEager && !inverseOne {
			continue
		}
		if _, err := em.preloadAssoc(ctx, a, loaded); err != nil {
			return err
		}
	}
	return nil
}

// preloadInverseOne loads the inverse side of a oneToOne association: the
// target rows whose foreign key points to the owners.
func (em *EntityManager) preloadInverseOne(ctx context.Context, a *mapping.Association, owners []any) ([]any, error) {
	inv := a.Inverse()
	jc := inv.JoinColumns[0]
	ref := a.Source.FieldByColumn(jc.Referenced)
	var keys []any
	byKey := map[any][]any{}
	for _, o := range owners {
		v := a.Source.Accessor().Get(o, ref.Pos)
		k := normalizeKey(v)
		if _, ok := byKey[k]; !ok {
			keys = append(keys, idArg{f: ref, v: v})
		}
		byKey[k] = append(byKey[k], o)
	}
	var all []any
	found := map[any]any{}
	for len(keys) > 0 {
		n := min(len(keys), preloadChunk)
		chunk := keys[:n]
		keys = keys[n:]
		res, err := em.loadWhere(ctx, a.Target, func(q *dbal.SelectQuery) {
			q.Where("? IN ?", dbal.Ident(jc.Name), dbal.In(chunk))
		}, 0)
		if err != nil {
			return nil, err
		}
		for _, t := range res {
			if owner := related(a.Target, t, inv); len(owner) > 0 {
				found[normalizeKey(a.Source.Accessor().Get(owner[0], ref.Pos))] = t
			}
			all = append(all, t)
		}
	}
	for k, os := range byKey {
		for _, o := range os {
			setToOne(a, o, found[k])
		}
	}
	return all, nil
}

// setToOne stores target (or nil) in a to-one association field.
func setToOne(a *mapping.Association, owner, target any) {
	addr := a.Source.Accessor().AssocAddr(owner, a.Pos)
	if a.Container == mapping.ContainerRef {
		addr.(refValue).setRefPtr(target)
		return
	}
	field := reflect.ValueOf(addr).Elem()
	if target == nil {
		field.SetZero()
		return
	}
	field.Set(reflect.ValueOf(target))
}
