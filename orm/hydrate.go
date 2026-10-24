package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
)

// whereID adds "id = ?" conditions for the identifier values.
func whereID(q *dbal.SelectQuery, meta *mapping.Entity, alias string, id []any) {
	for i, f := range meta.ID {
		col := f.Column
		if alias != "" {
			col = alias + "." + col
		}
		q.Where("? = ?", dbal.Ident(col), idArg{f: f, v: id[i]})
	}
}

// idArg converts lazily because the dialect is only known when rendering.
type idArg struct {
	f *mapping.Field
	v any
}

func (a idArg) AppendSQL(w *dbal.SQLWriter) error {
	v, err := a.f.Type.ToDatabase(convertTo(a.v, a.f.GoType), w.Dialect().Name())
	if err != nil {
		return err
	}
	return w.WriteArg(v)
}

// selectEntity builds SELECT <all columns> FROM <table> [AS alias].
func (em *EntityManager) selectEntity(meta *mapping.Entity, alias string) *dbal.SelectQuery {
	q := em.db.NewSelect()
	for _, f := range meta.Fields {
		col := f.Column
		if alias != "" {
			col = alias + "." + col
		}
		q.Column(col)
	}
	em.fromEntity(q, meta, alias)
	return q
}

// loadWhere selects entities of meta matching where. When into is given the
// single result is written into that entry (refresh/initialize).
func (em *EntityManager) loadWhere(ctx context.Context, meta *mapping.Entity, where func(*dbal.SelectQuery), limit int, into ...*entry) ([]any, error) {
	var force *entry
	if len(into) > 0 {
		force = into[0]
	}
	if meta.IsPolymorphic() && force == nil {
		return em.loadPolymorphic(ctx, meta, where, limit)
	}
	return em.loadConcrete(ctx, meta, where, limit, force)
}

// loadConcrete loads rows of exactly the class meta.
func (em *EntityManager) loadConcrete(ctx context.Context, meta *mapping.Entity, where func(*dbal.SelectQuery), limit int, force *entry) ([]any, error) {
	if force != nil {
		meta = force.meta
	}
	q := em.selectEntity(meta, "")
	where(q)
	for _, c := range append(em.filterConds(meta, ""), classConds(meta, "", true)...) {
		q.WhereExpr(c)
	}
	if limit > 0 {
		q.Limit(limit)
	}
	return em.loadQuery(ctx, meta, q, force)
}

// loadQuery runs q, which must select the columns of meta.Fields in order,
// and hydrates the rows.
func (em *EntityManager) loadQuery(ctx context.Context, meta *mapping.Entity, q *dbal.SelectQuery, force *entry) ([]any, error) {
	rows, err := q.Rows(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []any
	h := em.newHydrator(meta)
	defer func() { h.pending = nil }()
	for rows.Next() {
		ptr, err := h.row(ctx, rows, force)
		if err != nil {
			return nil, err
		}
		out = append(out, ptr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // free the connection before eager loading
	if err := em.resolvePending(ctx, h.pending); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		if err := em.preloadEager(ctx, meta, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// hydrator turns rows with the columns of meta.Fields (in order) into entities.
type hydrator struct {
	em      *EntityManager
	meta    *mapping.Entity
	slots   []slot
	dests   []any
	pending []pendingRef // to-one associations to polymorphic targets
}

type pendingRef struct {
	owner any
	assoc *mapping.Association
	id    any
}

type slot struct {
	f      *mapping.Field
	holder reflect.Value // pointer to a value of the scan type
	deref  bool          // holder is **T for a non-pointer T (NULL -> zero)
}

func (em *EntityManager) newHydrator(meta *mapping.Entity) *hydrator {
	return &hydrator{em: em, meta: meta}
}

// prepare sets up the scan destinations once per hydrator; later rows reuse
// them after zeroing (values are copied out in process).
func (h *hydrator) prepare() {
	if h.slots != nil {
		for _, s := range h.slots {
			s.holder.Elem().SetZero()
		}
		return
	}
	d := h.em.db.Dialect().Name()
	h.slots = make([]slot, len(h.meta.Fields))
	h.dests = make([]any, len(h.meta.Fields))
	for i, f := range h.meta.Fields {
		t := f.GoType
		s := slot{f: f}
		if f.Assoc != nil || (t.Kind() != reflect.Pointer && f.Nullable) {
			// Scan nullable non-pointer values (and foreign keys) through *T.
			if t.Kind() != reflect.Pointer {
				t = reflect.PointerTo(t)
			}
			s.deref = f.Assoc == nil
		}
		s.holder = reflect.New(t)
		h.slots[i] = s
		h.dests[i] = f.Type.ScanTarget(s.holder.Interface(), d)
	}
}

// row hydrates the current row. Managed, initialized entities in the identity
// map are returned unchanged unless force is that entity's entry.
func (h *hydrator) row(ctx context.Context, rows *sql.Rows, force *entry) (any, error) {
	h.prepare()
	if err := rows.Scan(h.dests...); err != nil {
		return nil, err
	}
	return h.process(ctx, force, true)
}

// values hydrates from cached database values (in Fields order).
func (h *hydrator) values(ctx context.Context, vals []any, force *entry) (any, error) {
	h.prepare()
	for i, v := range vals {
		if err := assign(h.dests[i], v); err != nil {
			return nil, fmt.Errorf("%s.%s: cached value: %w", h.meta.Name, h.meta.Fields[i].Column, err)
		}
	}
	return h.process(ctx, force, false)
}

// process fills the entity from the scanned slots.
func (h *hydrator) process(ctx context.Context, force *entry, cacheIt bool) (any, error) {
	id := make([]any, len(h.meta.ID))
	for i, f := range h.meta.ID {
		id[i] = h.slots[f.Pos].holder.Elem().Interface()
	}
	key := idKey(id)
	em := h.em
	e := force
	if e == nil {
		e = em.lookup(h.meta, key)
		if e != nil && e.initialized {
			if isA(e.meta, h.meta) {
				return view(e, h.meta), nil
			}
			return e.ptr, nil
		}
	}
	if e == nil {
		ptr := h.meta.Accessor().New()
		e = em.attach(h.meta, ptr, StateManaged)
		em.register(e, key)
	}
	acc := h.meta.Accessor()
	for _, s := range h.slots {
		v := s.holder.Elem()
		if s.f.Assoc != nil {
			h.setAssoc(e, s.f.Assoc, v)
			continue
		}
		if s.deref {
			if v.IsNil() {
				acc.Set(e.ptr, s.f.Pos, nil)
				continue
			}
			v = v.Elem()
		}
		acc.Set(e.ptr, s.f.Pos, v.Interface())
	}
	e.initialized = true
	e.state = StateManaged
	em.wireCollections(e)
	if err := em.takeSnapshot(e); err != nil {
		return nil, err
	}
	if cacheIt {
		em.cachePut(e)
	}
	if err := em.lifecycle(ctx, EventPostLoad, e, nil); err != nil {
		return nil, err
	}
	return e.ptr, nil
}

// setAssoc sets an owning to-one association from its foreign key value.
func (h *hydrator) setAssoc(e *entry, a *mapping.Association, fk reflect.Value) {
	if a.Target.IsPolymorphic() && !fk.IsNil() {
		// The class of the target is unknown until its row is read.
		h.pending = append(h.pending, pendingRef{owner: e.ptr, assoc: a, id: fk.Elem().Interface()})
		return
	}
	if a.Container == mapping.ContainerRef {
		r := h.meta.Accessor().AssocAddr(e.ptr, a.Pos).(refValue)
		if fk.IsNil() {
			r.setRefPtr(nil)
		} else {
			r.setRefPtr(h.em.reference(a.Target, []any{fk.Elem().Interface()}))
		}
		return
	}
	if a.Container != mapping.ContainerPointer {
		return
	}
	addr := reflect.ValueOf(h.meta.Accessor().AssocAddr(e.ptr, a.Pos)).Elem()
	if fk.IsNil() {
		addr.SetZero()
		return
	}
	ref := h.em.reference(a.Target, []any{fk.Elem().Interface()})
	addr.Set(reflect.ValueOf(ref))
}

// resolvePending loads polymorphic to-one targets and assigns them.
func (em *EntityManager) resolvePending(ctx context.Context, pending []pendingRef) error {
	byAssoc := map[*mapping.Association][]pendingRef{}
	var order []*mapping.Association
	for _, p := range pending {
		if _, ok := byAssoc[p.assoc]; !ok {
			order = append(order, p.assoc)
		}
		byAssoc[p.assoc] = append(byAssoc[p.assoc], p)
	}
	for _, a := range order {
		ps := byAssoc[a]
		idf := a.Target.SingleID()
		var ids []any
		for _, p := range ps {
			ids = append(ids, idArg{f: idf, v: p.id})
		}
		if _, err := em.loadWhere(ctx, a.Target, func(q *dbal.SelectQuery) {
			q.Where("? IN ?", dbal.Ident(idf.Column), dbal.In(ids))
		}, 0); err != nil {
			return err
		}
		for _, p := range ps {
			if e := em.lookup(a.Target, idKey([]any{p.id})); e != nil && isA(e.meta, a.Target) {
				setToOne(a, p.owner, view(e, a.Target))
			}
		}
	}
	return nil
}
