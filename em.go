package smorm

import (
	"context"
	"reflect"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/orm"
)

// EntityManager tracks entities and writes changes on Flush.
type EntityManager = orm.EntityManager

// EntityManagerOption configures an EntityManager.
type EntityManagerOption = orm.Option

// Entity manager options.
var (
	WithRegistry = orm.WithRegistry
	WithClock    = orm.WithClock
)

// Errors returned by the entity manager.
var (
	ErrNotFound  = orm.ErrNotFound
	ErrClosed    = orm.ErrClosed
	ErrNotEntity = orm.ErrNotEntity
)

// NewEntityManager returns an entity manager for db (*dbal.DB, *dbal.Conn or *dbal.Tx).
func NewEntityManager(db dbal.IDB, opts ...EntityManagerOption) *EntityManager {
	return orm.New(db, opts...)
}

// Find loads the entity of type T by identifier (ErrNotFound if missing).
func Find[T any](ctx context.Context, em *EntityManager, id ...any) (*T, error) {
	v, err := em.Find(ctx, reflect.TypeFor[T](), id...)
	if err != nil {
		return nil, err
	}
	return v.(*T), nil
}

// Reference returns a managed reference to the entity without loading it.
func Reference[T any](em *EntityManager, id ...any) (*T, error) {
	v, err := em.Reference(reflect.TypeFor[T](), id...)
	if err != nil {
		return nil, err
	}
	return v.(*T), nil
}

// Locking.
type (
	LockMode            = orm.LockMode
	OptimisticLockError = orm.OptimisticLockError
)

// Lock modes.
const (
	LockOptimistic       = orm.LockOptimistic
	LockPessimisticWrite = orm.LockPessimisticWrite
	LockPessimisticRead  = orm.LockPessimisticRead
)

// ErrOptimisticLock is matched (errors.Is) by optimistic lock failures.
var ErrOptimisticLock = orm.ErrOptimisticLock

// FindLocked loads T with a pessimistic lock inside a transaction.
func FindLocked[T any](ctx context.Context, em *EntityManager, mode LockMode, id ...any) (*T, error) {
	v, err := em.FindLocked(ctx, reflect.TypeFor[T](), mode, id...)
	if err != nil {
		return nil, err
	}
	return v.(*T), nil
}

// Filters.
type (
	Filter        = orm.Filter
	FilterFunc    = orm.FilterFunc
	SoftDeletable = orm.SoftDeletable
)

// ColumnFilter restricts entities having column to rows where it equals value.
var ColumnFilter = orm.ColumnFilter

// SoftDeleteFilterName names the built-in soft delete filter.
const SoftDeleteFilterName = orm.SoftDeleteFilterName

// Concrete returns the full instance behind an entity pointer, e.g. the
// *Car behind a *Vehicle from a polymorphic query (use a type switch).
func Concrete(ptr any) any { return orm.Concrete(ptr) }

// WithSecondLevelCache shares data of entities tagged cache between entity
// managers (see package cache for the in-memory implementation).
var WithSecondLevelCache = orm.WithSecondLevelCache
