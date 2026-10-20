package orm

import (
	"context"
	"errors"
	"fmt"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/internal/brand"
	"github.com/smatify/smorm/internal/entity"
)

// ErrDetached is wrapped by LazyLoadError when an entity is used after its
// entity manager was cleared, closed or garbage collected.
var ErrDetached = errors.New(brand.Name + ": entity is detached from its entity manager")

// LazyLoadError is the panic value raised when a getter touches a reference
// that cannot be loaded. Recover it with CatchLazy or the HTTP middleware,
// or load explicitly with Load to get an error instead.
type LazyLoadError struct {
	Entity string
	ID     any
	Err    error
}

func (e *LazyLoadError) Error() string {
	return fmt.Sprintf("%s: lazy loading %s(%v) failed: %v", brand.Name, e.Entity, e.ID, e.Err)
}

func (e *LazyLoadError) Unwrap() error { return e.Err }

// DefaultBatchSize is how many uninitialized references of the same type are
// loaded together when one of them is touched.
const DefaultBatchSize = 100

// WithContext sets the context used by lazy loading (getters cannot take
// one). Use the request context in web handlers.
func WithContext(ctx context.Context) Option { return func(em *EntityManager) { em.ctx = ctx } }

// WithBatchSize sets the lazy loading batch size (1 disables batching).
func WithBatchSize(n int) Option { return func(em *EntityManager) { em.batchSize = max(n, 1) } }

// SetContext replaces the lazy loading context.
func (em *EntityManager) SetContext(ctx context.Context) { em.ctx = ctx }

// Context returns the lazy loading context.
func (em *EntityManager) Context() context.Context { return em.ctx }

// EnsureLoaded implements entity.Loader: it loads an uninitialized reference
// and panics with *LazyLoadError if that is impossible.
func (e *entry) EnsureLoaded() {
	if e.initialized {
		return
	}
	em := e.manager()
	if em == nil || e.state == StateDetached {
		panic(&LazyLoadError{Entity: e.meta.Name, ID: e.key, Err: ErrDetached})
	}
	if em.closed {
		panic(&LazyLoadError{Entity: e.meta.Name, ID: e.key, Err: ErrClosed})
	}
	if err := em.initialize(em.ctx, e); err != nil {
		panic(&LazyLoadError{Entity: e.meta.Name, ID: e.key, Err: err})
	}
}

// initialize loads e and, for single column identifiers, up to batchSize-1
// other uninitialized references of the same type in one query.
func (em *EntityManager) initialize(ctx context.Context, e *entry) error {
	if _, ok, err := em.cacheGet(ctx, e.meta, e.key, e); err != nil || ok {
		return err
	}
	idf := e.meta.SingleID()
	if idf == nil || em.batchSize <= 1 {
		return em.refresh(ctx, e)
	}
	batch := []*entry{e}
	for _, other := range em.identity[e.meta.Root] {
		if other.meta != e.meta {
			continue
		}
		if len(batch) >= em.batchSize {
			break
		}
		if other != e && !other.initialized && other.state == StateManaged {
			batch = append(batch, other)
		}
	}
	ids := make([]any, len(batch))
	for i, b := range batch {
		ids[i] = idArg{f: idf, v: e.meta.Accessor().Get(b.ptr, idf.Pos)}
	}
	_, err := em.loadWhere(ctx, e.meta, func(q *dbal.SelectQuery) {
		q.Where("? IN ?", dbal.Ident(idf.Column), dbal.In(ids))
	}, 0)
	if err != nil {
		return err
	}
	if !e.initialized {
		return ErrNotFound
	}
	return nil
}

// Load initializes ptr if it is an uninitialized reference, returning an
// error instead of panicking. Other values are ignored.
func Load(ctx context.Context, ptr any) error {
	h := entity.Of(ptr)
	if h == nil {
		if l, ok := ptr.(interface{ load(context.Context) error }); ok {
			return l.load(ctx)
		}
		return nil
	}
	e, ok := entity.State(h).(*entry)
	if !ok || e.initialized {
		return nil
	}
	em := e.manager()
	if em == nil || e.state == StateDetached {
		return &LazyLoadError{Entity: e.meta.Name, ID: e.key, Err: ErrDetached}
	}
	if err := em.initialize(ctx, e); err != nil {
		return &LazyLoadError{Entity: e.meta.Name, ID: e.key, Err: err}
	}
	return nil
}

// CatchLazy runs fn and converts a *LazyLoadError panic into an error.
// Other panics are re-raised.
func CatchLazy(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			var le *LazyLoadError
			if e, ok := r.(error); ok && errors.As(e, &le) {
				err = le
				return
			}
			panic(r)
		}
	}()
	return fn()
}
