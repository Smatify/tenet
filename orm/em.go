package orm

import (
	"context"
	"fmt"
	"reflect"
	"time"
	"weak"

	"github.com/smatify/smorm/cache"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/mapping"
)

// EntityManager tracks entities and writes their changes on Flush. Like
// Doctrine's EntityManager it is a unit of work for one request or job and
// is not safe for concurrent use.
type EntityManager struct {
	db        dbal.IDB
	reg       *mapping.Registry
	now       func() time.Time
	identity  map[*mapping.Entity]map[any]*entry
	tracked   []*entry // insertion ordered; may contain detached entries until compacted
	closed    bool
	events    *EventManager
	ctx       context.Context
	batchSize int
	noLazy    bool
	lazyHook  func(LazyLoad)
	// txDepth counts running Transactional calls; their flushes count
	// postFlushes, which are dispatched after the commit.
	txDepth     int
	postFlushes int
	bulks       []*EventArgs // EventPostBulk waiting for the commit
	filters     *Filters
	cache       cache.Cache
	// resolving is set while references are loaded: a reference points to
	// a row that exists, so soft-deleted targets load too.
	resolving bool
}

// Option configures an EntityManager.
type Option func(*EntityManager)

// WithRegistry uses a custom metadata registry.
func WithRegistry(r *mapping.Registry) Option { return func(em *EntityManager) { em.reg = r } }

// WithClock replaces time.Now (used for timestamps).
func WithClock(now func() time.Time) Option { return func(em *EntityManager) { em.now = now } }

// New returns an entity manager working on db (a *dbal.DB, *dbal.Conn or *dbal.Tx).
func New(db dbal.IDB, opts ...Option) *EntityManager {
	em := &EntityManager{db: db, reg: mapping.Default, now: time.Now, identity: map[*mapping.Entity]map[any]*entry{}, events: NewEventManager(),
		ctx: context.Background(), batchSize: DefaultBatchSize, filters: newFilters()}
	for _, o := range opts {
		o(em)
	}
	return em
}

// DB returns the database handle.
func (em *EntityManager) DB() dbal.IDB { return em.db }

// Registry returns the metadata registry.
func (em *EntityManager) Registry() *mapping.Registry { return em.reg }

// IsOpen reports whether the entity manager can still be used.
func (em *EntityManager) IsOpen() bool { return !em.closed }

// Metadata returns the metadata for an entity type.
func (em *EntityManager) Metadata(t reflect.Type) (*mapping.Entity, error) { return em.reg.Get(t) }

// meta returns the metadata and validates that ptr is a non-nil pointer to an entity.
func (em *EntityManager) meta(ptr any) (*mapping.Entity, error) {
	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %T", ErrNotEntity, ptr)
	}
	return em.reg.Get(rv.Type())
}

func entryFromPtr(ptr any) (*entry, bool) {
	h := entity.Of(ptr)
	if h == nil {
		return nil, false
	}
	e, ok := entity.State(h).(*entry)
	return e, ok
}

// entryOf returns the state of ptr in this entity manager, or nil if unknown.
func (em *EntityManager) entryOf(meta *mapping.Entity, ptr any) *entry {
	h := meta.Accessor().Header(ptr)
	if e, ok := entity.State(h).(*entry); ok && e.manager() == em {
		return e
	}
	return nil
}

func (em *EntityManager) attach(meta *mapping.Entity, ptr any, state State) *entry {
	e := &entry{em: weak.Make(em), meta: meta, ptr: ptr, state: state, initialized: true}
	entity.SetState(meta.Accessor().Header(ptr), e)
	em.tracked = append(em.tracked, e)
	return e
}

func (em *EntityManager) register(e *entry, key any) {
	m := em.identity[e.meta.Root]
	if m == nil {
		m = map[any]*entry{}
		em.identity[e.meta.Root] = m
	}
	e.key = key
	m[key] = e
}

func (em *EntityManager) unregister(e *entry) {
	if e.key != nil {
		delete(em.identity[e.meta.Root], e.key)
		e.key = nil
	}
}

func (em *EntityManager) lookup(meta *mapping.Entity, key any) *entry {
	return em.identity[meta.Root][key]
}

// State returns the state of ptr.
func (em *EntityManager) State(ptr any) State {
	meta, err := em.meta(ptr)
	if err != nil {
		return StateNew
	}
	if e := em.entryOf(meta, ptr); e != nil {
		return e.state
	}
	if h, ok := entity.State(meta.Accessor().Header(ptr)).(*entry); ok && h.state != StateNew {
		return StateDetached
	}
	return StateNew
}

// Contains reports whether ptr is managed (or scheduled for insertion).
func (em *EntityManager) Contains(ptr any) bool {
	meta, err := em.meta(ptr)
	if err != nil {
		return false
	}
	e := em.entryOf(meta, ptr)
	return e != nil && (e.state == StateManaged || e.state == StateNew)
}

// idValues returns the identifier values of ptr.
func idValues(meta *mapping.Entity, ptr any) []any {
	acc := meta.Accessor()
	out := make([]any, len(meta.ID))
	for i, f := range meta.ID {
		out[i] = acc.Get(ptr, f.Pos)
	}
	return out
}

// entityKey returns the identity map key of ptr, or nil while an identifier
// is unset. A single identifier needs no slice.
func entityKey(meta *mapping.Entity, ptr any) any {
	if len(meta.ID) == 1 {
		v := meta.Accessor().Get(ptr, meta.ID[0].Pos)
		if isZeroID(v) {
			return nil
		}
		return normalizeKey(v)
	}
	if id := idValues(meta, ptr); hasID(id) {
		return idKey(id)
	}
	return nil
}

func hasID(vals []any) bool {
	for _, v := range vals {
		if isZeroID(v) {
			return false
		}
	}
	return true
}

// Find loads the entity of type t with the given identifier. Composite
// identifiers are passed in mapping order. It returns ErrNotFound when no
// row exists. Managed entities are returned from the identity map without a
// query.
func (em *EntityManager) Find(ctx context.Context, t reflect.Type, id ...any) (any, error) {
	if em.closed {
		return nil, ErrClosed
	}
	meta, err := em.reg.Get(t)
	if err != nil {
		return nil, err
	}
	if len(id) != len(meta.ID) {
		return nil, fmt.Errorf("%s: Find needs %d identifier values, got %d", meta.Name, len(meta.ID), len(id))
	}
	if e := em.lookup(meta, idKey(id)); e != nil {
		if e.state == StateRemoved || !isA(e.meta, meta) {
			return nil, ErrNotFound
		}
		if e.initialized {
			return view(e, meta), nil
		}
	}
	var force *entry
	if e := em.lookup(meta, idKey(id)); e != nil && !e.initialized && e.meta == meta {
		force = e
	}
	if ptr, ok, err := em.cacheGet(ctx, meta, idKey(id), force); err != nil || ok {
		return ptr, err
	}
	res, err := em.loadWhere(ctx, meta, func(q *dbal.SelectQuery) { whereID(q, meta, "", id) }, 1)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, ErrNotFound
	}
	return res[0], nil
}

// Reference returns a managed entity for the identifier without querying the
// database. If the entity is not in the identity map, an uninitialized
// reference (proxy) with only the identifier set is returned.
func (em *EntityManager) Reference(t reflect.Type, id ...any) (any, error) {
	meta, err := em.reg.Get(t)
	if err != nil {
		return nil, err
	}
	if len(id) != len(meta.ID) {
		return nil, fmt.Errorf("%s: Reference needs %d identifier values, got %d", meta.Name, len(meta.ID), len(id))
	}
	if meta.IsPolymorphic() {
		if e := em.lookup(meta, idKey(id)); e != nil && isA(e.meta, meta) {
			return view(e, meta), nil
		}
		return nil, fmt.Errorf("%s has subclasses; a reference cannot know its class, use Find", meta.Name)
	}
	return em.reference(meta, id), nil
}

func (em *EntityManager) reference(meta *mapping.Entity, id []any) any {
	key := idKey(id)
	if e := em.lookup(meta, key); e != nil {
		if isA(e.meta, meta) {
			return view(e, meta)
		}
		return e.ptr
	}
	acc := meta.Accessor()
	ptr := acc.New()
	for i, f := range meta.ID {
		acc.Set(ptr, f.Pos, convertTo(id[i], f.GoType))
	}
	e := em.attach(meta, ptr, StateManaged)
	e.initialized = false
	em.register(e, key)
	return ptr
}

// IsInitialized reports whether ptr is fully loaded (false for references
// that have not been loaded yet).
func (em *EntityManager) IsInitialized(ptr any) bool {
	meta, err := em.meta(ptr)
	if err != nil {
		return false
	}
	e := em.entryOf(meta, ptr)
	return e == nil || e.initialized
}

// Initialize loads an uninitialized reference.
func (em *EntityManager) Initialize(ctx context.Context, ptr any) error {
	meta, err := em.meta(ptr)
	if err != nil {
		return err
	}
	e := em.entryOf(meta, ptr)
	if e == nil || e.initialized {
		return nil
	}
	return em.refresh(ctx, e)
}

// Refresh reloads ptr from the database, discarding unflushed changes.
func (em *EntityManager) Refresh(ctx context.Context, ptr any) error {
	if em.closed {
		return ErrClosed
	}
	meta, err := em.meta(ptr)
	if err != nil {
		return err
	}
	e := em.entryOf(meta, ptr)
	if e == nil || e.state != StateManaged {
		return fmt.Errorf("%s: Refresh requires a managed entity", meta.Name)
	}
	if err := em.refresh(ctx, e); err != nil {
		return err
	}
	return em.cascade(e, mapping.CascadeRefresh, func(m *mapping.Entity, p any) error {
		if c := em.entryOf(m, p); c != nil && c.state == StateManaged {
			return em.refresh(ctx, c)
		}
		return nil
	})
}

func (em *EntityManager) refresh(ctx context.Context, e *entry) error {
	id := idValues(e.meta, e.ptr)
	res, err := em.loadWhere(ctx, e.meta, func(q *dbal.SelectQuery) { whereID(q, e.meta, "", id) }, 1, e)
	if err != nil {
		return err
	}
	if len(res) == 0 {
		return ErrNotFound
	}
	return nil
}

// Persist schedules a new entity for insertion (or keeps a managed one
// managed). Associations with cascade:persist are persisted too.
func (em *EntityManager) Persist(ptr any) error {
	if em.closed {
		return ErrClosed
	}
	meta, err := em.meta(ptr)
	if err != nil {
		return err
	}
	return em.persist(meta, ptr, map[any]bool{})
}

func (em *EntityManager) persist(meta *mapping.Entity, ptr any, seen map[any]bool) error {
	if seen[ptr] {
		return nil
	}
	seen[ptr] = true
	e := em.entryOf(meta, ptr)
	switch {
	case e == nil:
		if h, ok := entity.State(meta.Accessor().Header(ptr)).(*entry); ok && h.manager() != em {
			return fmt.Errorf("%s: cannot persist an entity managed by another entity manager; use Merge", meta.Name)
		}
		if err := em.scheduleInsert(meta, ptr); err != nil {
			return err
		}
	case e.state == StateRemoved:
		e.state = StateManaged
	case e.state == StateDetached:
		return fmt.Errorf("%s: cannot persist a detached entity", meta.Name)
	}
	return em.cascade(em.entryOf(meta, ptr), mapping.CascadePersist, func(m *mapping.Entity, p any) error {
		return em.persist(m, p, seen)
	})
}

func (em *EntityManager) scheduleInsert(meta *mapping.Entity, ptr any) error {
	if err := assignPreInsertID(meta, ptr); err != nil {
		return err
	}
	key := entityKey(meta, ptr)
	if key != nil {
		if other := em.lookup(meta, key); other != nil && other.ptr != ptr {
			return fmt.Errorf("%s: another entity with identifier %v is already managed", meta.Name, idValues(meta, ptr))
		}
	} else if meta.Generator == mapping.GenNone {
		return fmt.Errorf("%s: entities with application assigned identifiers need the ID set before Persist", meta.Name)
	}
	e := em.attach(meta, ptr, StateNew)
	if key != nil {
		em.register(e, key)
	}
	return nil
}

// Remove schedules a managed entity for deletion; new entities are simply
// forgotten. Associations with cascade:remove are removed too.
func (em *EntityManager) Remove(ptr any) error {
	if em.closed {
		return ErrClosed
	}
	meta, err := em.meta(ptr)
	if err != nil {
		return err
	}
	return em.remove(meta, ptr, map[any]bool{})
}

func (em *EntityManager) remove(meta *mapping.Entity, ptr any, seen map[any]bool) error {
	if seen[ptr] {
		return nil
	}
	seen[ptr] = true
	e := em.entryOf(meta, ptr)
	if e == nil {
		return nil // unknown entities are ignored, like Doctrine
	}
	if err := em.cascade(e, mapping.CascadeRemove, func(m *mapping.Entity, p any) error {
		return em.remove(m, p, seen)
	}); err != nil {
		return err
	}
	switch e.state {
	case StateNew:
		em.forget(e)
	case StateManaged:
		e.state = StateRemoved
	}
	return nil
}

// forget detaches e and clears its header.
func (em *EntityManager) forget(e *entry) {
	em.unregister(e)
	e.state = StateDetached
	entity.SetState(e.meta.Accessor().Header(e.ptr), nil)
}

// Detach stops tracking ptr (and associations with cascade:detach).
// Unflushed changes are not written.
func (em *EntityManager) Detach(ptr any) error {
	meta, err := em.meta(ptr)
	if err != nil {
		return err
	}
	return em.detach(meta, ptr, map[any]bool{})
}

func (em *EntityManager) detach(meta *mapping.Entity, ptr any, seen map[any]bool) error {
	if seen[ptr] {
		return nil
	}
	seen[ptr] = true
	e := em.entryOf(meta, ptr)
	if e == nil {
		return nil
	}
	if err := em.cascade(e, mapping.CascadeDetach, func(m *mapping.Entity, p any) error { return em.detach(m, p, seen) }); err != nil {
		return err
	}
	em.unregister(e)
	e.state = StateDetached
	return nil
}

// Clear detaches every entity.
func (em *EntityManager) Clear() {
	for _, e := range em.tracked {
		if e.state != StateDetached {
			em.unregister(e)
			e.state = StateDetached
		}
	}
	em.tracked = nil
	em.identity = map[*mapping.Entity]map[any]*entry{}
}

// cascade calls fn for every entity reachable through associations of e that
// cascade op. Uninitialized references are skipped.
func (em *EntityManager) cascade(e *entry, op mapping.Cascade, fn func(*mapping.Entity, any) error) error {
	if e == nil || !e.initialized {
		return nil
	}
	for _, a := range e.meta.Assocs {
		if !a.Cascade.Has(op) {
			continue
		}
		for _, p := range related(e.meta, e.ptr, a) {
			if err := fn(a.Target, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// related returns the entities held by association a of ptr (without
// loading lazy collections).
// relatedOne returns the first entity association a of ptr points to, or
// nil; unlike related it allocates nothing for to-one associations.
func relatedOne(meta *mapping.Entity, ptr any, a *mapping.Association) any {
	switch a.Container {
	case mapping.ContainerPointer:
		v := meta.Accessor().GetAssoc(ptr, a.Pos)
		if v == nil || reflect.ValueOf(v).IsNil() {
			return nil
		}
		return v
	case mapping.ContainerRef:
		if p := meta.Accessor().AssocAddr(ptr, a.Pos).(refValue).refPtr(); p != nil {
			return p
		}
		return nil
	}
	if rel := related(meta, ptr, a); len(rel) > 0 {
		return rel[0]
	}
	return nil
}

func related(meta *mapping.Entity, ptr any, a *mapping.Association) []any {
	acc := meta.Accessor()
	switch a.Container {
	case mapping.ContainerPointer:
		v := acc.GetAssoc(ptr, a.Pos)
		if v == nil || reflect.ValueOf(v).IsNil() {
			return nil
		}
		return []any{v}
	case mapping.ContainerSlice:
		rv := reflect.ValueOf(acc.GetAssoc(ptr, a.Pos))
		out := make([]any, 0, rv.Len())
		for i := range rv.Len() {
			if el := rv.Index(i); !el.IsNil() {
				out = append(out, el.Interface())
			}
		}
		return out
	case mapping.ContainerRef:
		if p := acc.AssocAddr(ptr, a.Pos).(refValue).refPtr(); p != nil {
			return []any{p}
		}
	case mapping.ContainerCollection:
		return acc.AssocAddr(ptr, a.Pos).(collection).Entities()
	}
	return nil
}

// Transactional runs fn in a transaction and flushes before committing. If
// fn or the flush fails, the transaction is rolled back and listeners get
// onRollback; postFlush of the flushes inside runs after the commit.
func (em *EntityManager) Transactional(ctx context.Context, fn func(ctx context.Context, em *EntityManager) error) error {
	if em.closed {
		return ErrClosed
	}
	em.txDepth++
	err := em.db.RunInTx(ctx, nil, func(ctx context.Context, tx *dbal.Tx) error {
		prev := em.db
		em.db = tx
		defer func() { em.db = prev }()
		if err := fn(ctx, em); err != nil {
			return err
		}
		return em.Flush(ctx)
	})
	em.txDepth--
	if em.txDepth > 0 {
		return err
	}
	// postFlush waits for the commit of the outermost transaction.
	n, bulks := em.postFlushes, em.bulks
	em.postFlushes, em.bulks = 0, nil
	if err != nil {
		if n > 0 || em.closed {
			em.rollbackEvent(ctx)
		}
		return err
	}
	for range n {
		if err := em.events.dispatch(ctx, EventPostFlush, &EventArgs{EM: em}); err != nil {
			return err
		}
	}
	for _, args := range bulks {
		if err := em.events.dispatch(ctx, EventPostBulk, args); err != nil {
			return err
		}
	}
	return nil
}

// convertTo converts v to type t when possible (e.g. int to int64 for IDs).
func convertTo(v any, t reflect.Type) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	if rv.Type() == t {
		return v
	}
	if rv.Type().ConvertibleTo(t) {
		return rv.Convert(t).Interface()
	}
	return v
}
