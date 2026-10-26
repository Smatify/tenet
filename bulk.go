package smorm

import (
	"context"
	"reflect"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/orm"
)

// UpdateQuery changes many rows of T in one statement (see Update).
type UpdateQuery[T any] struct{ u *orm.Update }

// Update starts a bulk update of T (alias "t0"), e.g.
//
//	smorm.Update[Thread](em).Set("PostCount", smorm.Raw("{PostCount} + 1")).Where(smorm.Eq("ID", id)).Exec(ctx)
//
// It bypasses the unit of work: loaded entities keep their values; hooks,
// versions and timestamps are not touched. Listeners get EventPostBulk with
// the IDs of the changed rows once the change is committed.
func Update[T any](em *EntityManager) *UpdateQuery[T] {
	return &UpdateQuery[T]{u: em.NewUpdate(reflect.TypeFor[T]())}
}

// Set assigns a Go value or an expression (Raw) to field.
func (q *UpdateQuery[T]) Set(field string, value any) *UpdateQuery[T] {
	q.u.Set(field, value)
	return q
}

// From adds a source of the new values as alias, e.g. a Query.Sub()
// (Postgres and SQLite only).
func (q *UpdateQuery[T]) From(src dbal.Expr, alias string) *UpdateQuery[T] {
	q.u.From(src, alias)
	return q
}

// Where adds conditions joined with AND; an update needs one.
func (q *UpdateQuery[T]) Where(conds ...Cond) *UpdateQuery[T] { q.u.Where(conds...); return q }

// WithoutFilter disables the named filters for this update.
func (q *UpdateQuery[T]) WithoutFilter(names ...string) *UpdateQuery[T] {
	q.u.WithoutFilter(names...)
	return q
}

// SQL renders the update for debugging.
func (q *UpdateQuery[T]) SQL() (string, []any, error) { return q.u.SQL() }

// Exec runs the update and returns the number of changed rows.
func (q *UpdateQuery[T]) Exec(ctx context.Context) (int, error) { return q.u.Exec(ctx) }
