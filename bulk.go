package tenet

import (
	"context"
	"reflect"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/orm"
)

// UpdateQuery changes many rows of T in one statement (see Update).
type UpdateQuery[T any] struct{ u *orm.Update }

// Update starts a bulk update of T (alias "t0"), e.g.
//
//	tenet.Update[Thread](em).Set("PostCount", tenet.Raw("{PostCount} + 1")).Where(tenet.Eq("ID", id)).Exec(ctx)
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

// InsertQuery adds many rows of T in one statement (see Insert).
type InsertQuery[T any] struct{ i *orm.Insert }

// Selection is a query whose rows Insert.Select inserts: a Query of any
// type with Columns.
type Selection interface{ ormQuery() *orm.Query }

// Insert starts a bulk insert of T (alias "t0"): entities (Rows) or the
// rows of a query (Select), optionally an upsert, e.g.
//
//	tenet.Insert[Read](em).Rows(r).OnConflict("User", "Thread").Set("ReadAt", tenet.Raw("GREATEST({ReadAt}, {excluded.ReadAt})")).Exec(ctx)
//
// It bypasses the unit of work: the entities are not managed afterwards and
// hooks do not run; IDs and timestamps are assigned as on a flush.
// Listeners get EventPostBulk with the IDs written. Postgres and SQLite only.
func Insert[T any](em *EntityManager) *InsertQuery[T] {
	return &InsertQuery[T]{i: em.NewInsert(reflect.TypeFor[T]())}
}

// Rows adds entities as rows.
func (q *InsertQuery[T]) Rows(entities ...*T) *InsertQuery[T] {
	for _, e := range entities {
		q.i.Rows(e)
	}
	return q
}

// Select inserts the rows of a query with Columns into fields, in order.
func (q *InsertQuery[T]) Select(src Selection, fields ...string) *InsertQuery[T] {
	q.i.Select(src.ormQuery(), fields...)
	return q
}

// OnConflict names the fields of the unique key a row may collide with.
func (q *InsertQuery[T]) OnConflict(fields ...string) *InsertQuery[T] {
	q.i.OnConflict(fields...)
	return q
}

// DoNothing keeps the existing row on a conflict.
func (q *InsertQuery[T]) DoNothing() *InsertQuery[T] { q.i.DoNothing(); return q }

// Set updates field of the existing row on a conflict; {excluded.Field} is
// the value proposed.
func (q *InsertQuery[T]) Set(field string, value any) *InsertQuery[T] {
	q.i.Set(field, value)
	return q
}

// SQL renders the insert for debugging.
func (q *InsertQuery[T]) SQL() (string, []any, error) { return q.i.SQL() }

// Exec runs the insert and returns the number of rows written.
func (q *InsertQuery[T]) Exec(ctx context.Context) (int, error) { return q.i.Exec(ctx) }

// DeleteQuery removes many rows of T in one statement (see Delete).
type DeleteQuery[T any] struct{ d *orm.Delete }

// Delete starts a bulk delete of T (alias "t0"). It bypasses the unit of
// work: loaded entities stay managed, cascades and hooks do not run (the
// database's ON DELETE rules do). Listeners get EventPostBulk with the IDs
// of the deleted rows.
func Delete[T any](em *EntityManager) *DeleteQuery[T] {
	return &DeleteQuery[T]{d: em.NewDelete(reflect.TypeFor[T]())}
}

// Where adds conditions joined with AND; a delete needs one.
func (q *DeleteQuery[T]) Where(conds ...Cond) *DeleteQuery[T] { q.d.Where(conds...); return q }

// WithoutFilter disables the named filters for this delete.
func (q *DeleteQuery[T]) WithoutFilter(names ...string) *DeleteQuery[T] {
	q.d.WithoutFilter(names...)
	return q
}

// SQL renders the delete for debugging.
func (q *DeleteQuery[T]) SQL() (string, []any, error) { return q.d.SQL() }

// Exec runs the delete and returns the number of deleted rows.
func (q *DeleteQuery[T]) Exec(ctx context.Context) (int, error) { return q.d.Exec(ctx) }
