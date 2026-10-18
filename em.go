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
