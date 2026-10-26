package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/mapping"
)

// Update changes many rows of an entity type in one statement, using
// field names instead of columns. It bypasses the unit of work: entities
// already loaded keep their values, lifecycle hooks, versions and
// timestamps are not touched (set them like any field). After the change
// is committed, listeners get EventPostBulk with the IDs of the rows.
// Build it with EntityManager.NewUpdate.
type Update struct {
	q    *Query
	sets []bulkSet
	from []bulkFrom
}

type bulkSet struct {
	field string
	value any
}

type bulkFrom struct {
	src   dbal.Expr
	alias string
}

// NewUpdate starts a bulk update of the entity type t (alias "t0").
func (em *EntityManager) NewUpdate(t reflect.Type) *Update {
	q := em.NewQuery(t)
	if q.err == nil && q.meta.Inheritance == mapping.Joined {
		q.fail(fmt.Errorf("update: %s uses joined inheritance", q.meta.Name))
	}
	return &Update{q: q}
}

// Set assigns value to field: a Go value (converted like a condition
// value) or an expression, e.g. Raw("{PostCount} + 1") or Raw("v.n").
func (u *Update) Set(field string, value any) *Update {
	u.sets = append(u.sets, bulkSet{field, value})
	return u
}

// From adds a source of the new values as alias, e.g. a Query.Sub()
// (UPDATE … FROM; Postgres and SQLite only).
func (u *Update) From(src dbal.Expr, alias string) *Update {
	u.from = append(u.from, bulkFrom{src, alias})
	return u
}

// Where adds conditions joined with AND; an update needs one.
func (u *Update) Where(conds ...Cond) *Update { u.q.Where(conds...); return u }

// WithoutFilter disables the named filters for this update.
func (u *Update) WithoutFilter(names ...string) *Update { u.q.WithoutFilter(names...); return u }

func (u *Update) build() (*dbal.UpdateQuery, error) {
	q := u.q
	if q.err != nil {
		return nil, q.err
	}
	if len(q.where) == 0 {
		return nil, errors.New("update: no condition (Where)")
	}
	if len(u.sets) == 0 {
		return nil, errors.New("update: nothing to set")
	}
	meta := q.meta
	out := q.em.db.NewUpdate().TableAs(meta.Table, RootAlias)
	for _, s := range u.sets {
		_, f, err := q.resolve(s.field)
		if err != nil {
			return nil, err
		}
		var v any
		switch e := s.value.(type) {
		case Cond:
			if v, err = e.build(q); err != nil {
				return nil, err
			}
		case dbal.Expr:
			v = e
		default:
			if v, err = q.value(f, s.value); err != nil {
				return nil, err
			}
		}
		out.Set(f.Column, v)
	}
	for _, f := range u.from {
		out.From("? AS ?", f.src, dbal.Ident(f.alias))
	}
	where, err := And(q.where...).build(q)
	if err != nil {
		return nil, err
	}
	out.WhereExpr(where)
	for _, c := range append(q.em.filterCondsExcept(meta, RootAlias, q.without), classConds(meta, RootAlias, !meta.IsPolymorphic())...) {
		out.WhereExpr(c)
	}
	if id := u.returning(); id != nil {
		col := RootAlias + "." + id.Column
		if q.em.db.Dialect().Name() == dialect.SQLite { // RETURNING knows no aliases
			col = id.Column
		}
		out.Returning(col)
	}
	return out, nil
}

// returning is the identifier the update returns, nil when the type has
// no single identifier or the database cannot return rows of an UPDATE.
func (u *Update) returning() *mapping.Field {
	if !u.q.em.db.Dialect().Features().Has(dialect.FeatureReturning) {
		return nil
	}
	return u.q.meta.SingleID()
}

// SQL renders the update for debugging.
func (u *Update) SQL() (string, []any, error) {
	s, err := u.build()
	if err != nil {
		return "", nil, err
	}
	return s.Build()
}

// Exec runs the update and returns the number of changed rows (matched
// rows on MySQL).
func (u *Update) Exec(ctx context.Context) (int, error) {
	em := u.q.em
	if em.closed {
		return 0, ErrClosed
	}
	s, err := u.build()
	if err != nil {
		return 0, err
	}
	meta := u.q.meta
	id := u.returning()
	if id == nil {
		res, err := s.Exec(ctx)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		return int(n), em.bulk(ctx, &EventArgs{EM: em, Meta: meta})
	}
	rows, err := s.Rows(ctx)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []any
	for rows.Next() {
		v := reflect.New(id.GoType)
		if err := rows.Scan(id.Type.ScanTarget(v.Interface(), em.db.Dialect().Name())); err != nil {
			return 0, err
		}
		ids = append(ids, v.Elem().Interface())
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return len(ids), em.bulk(ctx, &EventArgs{EM: em, Meta: meta, IDs: ids})
}

// bulk dispatches EventPostBulk now, or after the commit of the running
// Transactional.
func (em *EntityManager) bulk(ctx context.Context, args *EventArgs) error {
	if em.txDepth > 0 {
		em.bulks = append(em.bulks, args)
		return nil
	}
	return em.events.dispatch(ctx, EventPostBulk, args)
}
