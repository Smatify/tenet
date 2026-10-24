package orm

import (
	"context"
	"iter"
	"reflect"
	"slices"

	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/mapping"
)

// Collection holds the entities of a to-many association. A collection of a
// loaded entity initializes itself on first use (lazy loading); with
// fetch:extraLazy, Len and Contains query the database without loading.
// The zero value is an empty, initialized collection.
type Collection[T any] struct {
	items []*T
	st    *collState
}

// collState links a collection to its owner. nil means a plain collection.
type collState struct {
	initialized bool
	owner       *entry
	assoc       *mapping.Association
	snapshot    []any // items when loaded or last flushed
	added       []any // added before initialization (extra lazy)
	removed     []any // removed items, candidates for orphan removal
}

// NewCollection returns an initialized collection with items.
func NewCollection[T any](items ...*T) Collection[T] { return Collection[T]{items: items} }

// CollectionElem implements mapping.CollectionField.
func (Collection[T]) CollectionElem() reflect.Type { return reflect.TypeFor[T]() }

// IsInitialized reports whether the items are loaded.
func (c *Collection[T]) IsInitialized() bool { return c.st == nil || c.st.initialized }

func (c *Collection[T]) ensure() {
	if c.IsInitialized() {
		return
	}
	st := c.st
	if em := st.owner.manager(); em != nil && !em.closed {
		if err := em.lazy(st.owner.meta.Name+"."+st.assoc.Name, st.owner.key, st.owner.meta.GoType); err != nil {
			panic(&LazyLoadError{Entity: st.owner.meta.Name + "." + st.assoc.Name, ID: st.owner.key, Err: err})
		}
	}
	if err := c.load(st.owner.manager().lazyContext()); err != nil {
		panic(err)
	}
}

// load initializes the collection, returning a *LazyLoadError on failure.
func (c *Collection[T]) load(ctx context.Context) error {
	if c.IsInitialized() {
		return nil
	}
	st := c.st
	em := st.owner.manager()
	fail := func(err error) error {
		return &LazyLoadError{Entity: st.owner.meta.Name + "." + st.assoc.Name, ID: st.owner.key, Err: err}
	}
	switch {
	case em == nil || st.owner.state == StateDetached:
		return fail(ErrDetached)
	case em.closed:
		return fail(ErrClosed)
	}
	loaded, err := em.loadCollection(ctx, st.owner, st.assoc)
	if err != nil {
		return fail(err)
	}
	items := make([]*T, 0, len(loaded)+len(st.added))
	for _, p := range loaded {
		items = append(items, p.(*T))
	}
	for _, p := range st.added {
		if !slices.Contains(items, p.(*T)) {
			items = append(items, p.(*T))
		}
	}
	c.items = items
	st.snapshot = loaded
	st.added = nil
	st.initialized = true
	return nil
}

// All loads the collection and returns a copy of its items.
func (c *Collection[T]) All() []*T { c.ensure(); return slices.Clone(c.items) }

// Iter loads the collection and iterates over its items.
func (c *Collection[T]) Iter() iter.Seq[*T] {
	c.ensure()
	return slices.Values(slices.Clone(c.items))
}

// Get returns the i-th item.
func (c *Collection[T]) Get(i int) *T { c.ensure(); return c.items[i] }

// Len returns the number of items. Extra lazy collections count in the
// database without loading.
func (c *Collection[T]) Len() int {
	if !c.IsInitialized() && c.st.assoc.Fetch == mapping.FetchExtraLazy {
		em := c.st.owner.manager()
		if em != nil {
			n, err := em.countCollection(em.lazyContext(), c.st.owner, c.st.assoc)
			if err != nil {
				panic(&LazyLoadError{Entity: c.st.owner.meta.Name + "." + c.st.assoc.Name, ID: c.st.owner.key, Err: err})
			}
			return n + len(c.st.added)
		}
	}
	c.ensure()
	return len(c.items)
}

// Contains reports whether item is in the collection.
func (c *Collection[T]) Contains(item *T) bool {
	if !c.IsInitialized() && slices.Contains(c.st.added, any(item)) {
		return true
	}
	c.ensure()
	return slices.Contains(c.items, item)
}

// Add appends items. Extra lazy collections do not load for this.
func (c *Collection[T]) Add(items ...*T) {
	if !c.IsInitialized() && c.st.assoc.Fetch == mapping.FetchExtraLazy {
		for _, it := range items {
			c.st.added = append(c.st.added, it)
		}
		return
	}
	c.ensure()
	for _, it := range items {
		if !slices.Contains(c.items, it) {
			c.items = append(c.items, it)
		}
	}
}

// Remove deletes item from the collection and reports whether it was there.
// With orphanRemoval the entity is deleted on the next flush.
func (c *Collection[T]) Remove(item *T) bool {
	c.ensure()
	i := slices.Index(c.items, item)
	if i < 0 {
		return false
	}
	c.items = slices.Delete(c.items, i, i+1)
	if c.st != nil {
		c.st.removed = append(c.st.removed, item)
	}
	return true
}

// Clear removes every item.
func (c *Collection[T]) Clear() {
	c.ensure()
	if c.st != nil {
		for _, it := range c.items {
			c.st.removed = append(c.st.removed, it)
		}
	}
	c.items = nil
}

// Entities returns the items known without loading (for cascades).
func (c *Collection[T]) Entities() []any {
	out := make([]any, 0, len(c.items))
	for _, it := range c.items {
		out = append(out, it)
	}
	if c.st != nil {
		out = append(out, c.st.added...)
	}
	return out
}

// collection is the type-erased view the unit of work uses.
type collection interface {
	Entities() []any
	IsInitialized() bool
	state() *collState
	setState(*collState)
	fill(items []any)
	load(ctx context.Context) error
}

func (c *Collection[T]) state() *collState { return c.st }

func (c *Collection[T]) fill(items []any) {
	c.items = make([]*T, len(items))
	for i, it := range items {
		c.items[i] = it.(*T)
	}
}
func (c *Collection[T]) setState(s *collState) { c.st = s }

// Ref is a reference to a single entity for Go style entities with exported
// fields. Get loads the entity on first use.
type Ref[T any] struct{ p *T }

// NewRef returns a reference to p.
func NewRef[T any](p *T) Ref[T] { return Ref[T]{p: p} }

// RefElem implements mapping.RefField.
func (Ref[T]) RefElem() reflect.Type { return reflect.TypeFor[T]() }

// Get returns the entity, loading it if needed (panics with *LazyLoadError
// on failure). It returns nil for an empty reference.
func (r Ref[T]) Get() *T {
	if r.p != nil {
		entity.Touch(r.p)
	}
	return r.p
}

// Ptr returns the entity without loading it.
func (r Ref[T]) Ptr() *T { return r.p }

// IsNil reports whether the reference is empty.
func (r Ref[T]) IsNil() bool { return r.p == nil }

// Set points the reference to p.
func (r *Ref[T]) Set(p *T) { r.p = p }

func (r Ref[T]) refPtr() any {
	if r.p == nil {
		return nil
	}
	return r.p
}
func (r *Ref[T]) setRefPtr(p any) {
	if p == nil {
		r.p = nil
		return
	}
	r.p = p.(*T)
}

// load initializes the referenced entity.
func (r *Ref[T]) load(ctx context.Context) error {
	if r.p == nil {
		return nil
	}
	return Load(ctx, r.p)
}

type refValue interface {
	refPtr() any
	setRefPtr(any)
}

func (em *EntityManager) lazyContext() context.Context {
	if em == nil || em.ctx == nil {
		return context.Background()
	}
	return em.ctx
}
