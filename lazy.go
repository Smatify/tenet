package smorm

import (
	"context"
	"errors"
	"net/http"

	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/orm"
)

// Touch loads p if it is an uninitialized reference (a proxy). Getters of
// entities call it before reading a field:
//
//	func (p *Post) Title() string { smorm.Touch(p); return p.title }
//
// If loading fails, Touch panics with *LazyLoadError; see CatchLazy and
// RecoverLazyLoad.
func Touch(p any) { entity.Touch(p) }

// Load initializes p (an entity reference, Collection or Ref) and returns
// an error instead of panicking.
func Load(ctx context.Context, p any) error { return orm.Load(ctx, p) }

// LazyLoadError is the panic value raised by failed lazy loading.
type LazyLoadError = orm.LazyLoadError

// ErrDetached is wrapped by LazyLoadError for entities whose manager is gone.
var ErrDetached = orm.ErrDetached

// ErrLazyLoadingDisabled is wrapped by LazyLoadError when an entity manager
// created WithoutLazyLoading would have to load lazily.
var ErrLazyLoadingDisabled = orm.ErrLazyLoadingDisabled

// LazyLoad describes a lazy load for WithLazyLoadHook.
type LazyLoad = orm.LazyLoad

// Lazy loading options.
var (
	WithContext        = orm.WithContext
	WithBatchSize      = orm.WithBatchSize
	WithoutLazyLoading = orm.WithoutLazyLoading
	WithLazyLoadHook   = orm.WithLazyLoadHook
)

// CatchLazy runs fn and converts a lazy loading panic into an error.
func CatchLazy(fn func() error) error { return orm.CatchLazy(fn) }

// RecoverLazyLoad is HTTP middleware that turns lazy loading panics into a
// response. onError defaults to a plain 500 response.
func RecoverLazyLoad(next http.Handler, onError func(w http.ResponseWriter, r *http.Request, err *LazyLoadError)) http.Handler {
	if onError == nil {
		onError = func(w http.ResponseWriter, _ *http.Request, _ *LazyLoadError) {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				var le *LazyLoadError
				if err, ok := rec.(error); ok && errors.As(err, &le) {
					onError(w, r, le)
					return
				}
				panic(rec)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Collection holds the entities of a to-many association; see orm.Collection.
type Collection[T any] = orm.Collection[T]

// Ref references a single entity in Go style entities; see orm.Ref.
type Ref[T any] = orm.Ref[T]

// NewCollection returns an initialized collection with items.
func NewCollection[T any](items ...*T) Collection[T] { return orm.NewCollection(items...) }

// NewRef returns a reference to p.
func NewRef[T any](p *T) Ref[T] { return orm.NewRef(p) }
