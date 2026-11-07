package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"reflect"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect"
	"github.com/smatify/tenet/mapping"
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
	out := q.em.db.NewUpdate().TableAs(meta.Table, q.root)
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
	for _, c := range append(q.em.filterCondsExcept(meta, q.root, q.without), classConds(meta, q.root, !meta.IsPolymorphic())...) {
		out.WhereExpr(c)
	}
	if id := u.returning(); id != nil {
		col := q.root + "." + id.Column
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
	return em.runBulk(ctx, u.q.meta, s, u.returning())
}

// scanIDs reads the identifiers a statement returned and closes rows.
func scanIDs(rows *sql.Rows, id *mapping.Field, d dialect.Name) ([]any, error) {
	defer rows.Close()
	var ids []any
	for rows.Next() {
		v := reflect.New(id.GoType)
		if err := rows.Scan(id.Type.ScanTarget(v.Interface(), d)); err != nil {
			return nil, err
		}
		ids = append(ids, v.Elem().Interface())
	}
	return ids, rows.Err()
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

// Insert adds many rows of an entity type: entities (Rows, RowsFrom) or
// the rows of a query (Select, one statement), optionally as an upsert
// (OnConflict). Entities are written in statements within the bind
// parameter limit of the database, several of them in one transaction.
// Like Update it bypasses the unit of work: the entities are not managed
// afterwards and lifecycle hooks do not run; identifiers and timestamps
// are assigned as on a flush. Listeners get EventPostBulk with the IDs of
// the rows written. Postgres and SQLite only. Build it with
// EntityManager.NewInsert.
type Insert struct {
	q         *Query
	rows      []any
	seq       iter.Seq[any]
	sel       *Query
	selFields []string
	target    []string
	nothing   bool
	sets      []bulkSet
}

// excludedAlias names the row proposed for insertion in OnConflict
// updates: {excluded.Field}.
const excludedAlias = "excluded"

// NewInsert starts a bulk insert of the entity type t (alias "t0").
func (em *EntityManager) NewInsert(t reflect.Type) *Insert {
	q := em.NewQuery(t)
	if q.err == nil {
		if q.meta.Inheritance == mapping.Joined {
			q.fail(fmt.Errorf("insert: %s uses joined inheritance", q.meta.Name))
		}
		q.aliases[excludedAlias] = q.meta
	}
	return &Insert{q: q}
}

// Rows adds entities (pointers) as rows.
func (i *Insert) Rows(entities ...any) *Insert { i.rows = append(i.rows, entities...); return i }

// RowsFrom adds the entities seq yields as rows, after those of Rows. Exec
// takes them while it writes, one statement at a time, so millions of rows
// need not be in memory at once. seq runs inside the transaction of Exec:
// it must not wait for another connection when the pool has only one.
func (i *Insert) RowsFrom(seq iter.Seq[any]) *Insert { i.seq = seq; return i }

// Select inserts the rows of a projection (Query.Columns) into fields, in
// order (INSERT … SELECT).
func (i *Insert) Select(q *Query, fields ...string) *Insert {
	i.sel, i.selFields = q, fields
	return i
}

// OnConflict names the fields of the unique key a row may collide with;
// DoNothing or Set decides what happens then.
func (i *Insert) OnConflict(fields ...string) *Insert { i.target = fields; return i }

// DoNothing keeps the existing row on a conflict.
func (i *Insert) DoNothing() *Insert { i.nothing = true; return i }

// Set updates field of the existing row on a conflict: a Go value or an
// expression, e.g. Raw("GREATEST({ReadAt}, {excluded.ReadAt})").
func (i *Insert) Set(field string, value any) *Insert {
	i.sets = append(i.sets, bulkSet{field, value})
	return i
}

// check reports whether the insert can be built at all.
func (i *Insert) check() error {
	if i.q.err != nil {
		return i.q.err
	}
	if d := i.q.em.db.Dialect().Name(); d != dialect.Postgres && d != dialect.SQLite {
		return fmt.Errorf("insert: not supported by %s", d)
	}
	if (len(i.rows) > 0 || i.seq != nil) && i.sel != nil {
		return errors.New("insert: rows and a select at once")
	}
	return nil
}

// build renders the insert of rows (or of the select) on db.
func (i *Insert) build(db dbal.IDB, rows []any) (*dbal.InsertQuery, error) {
	if err := i.check(); err != nil {
		return nil, err
	}
	q := i.q
	em, meta := q.em, q.meta
	out := db.NewInsert().TableAs(meta.Table, q.root)
	switch {
	case i.sel != nil:
		cols := make([]string, len(i.selFields))
		for n, name := range i.selFields {
			_, f, err := q.resolve(name)
			if err != nil {
				return nil, err
			}
			cols[n] = f.Column
		}
		sel, err := i.sel.build(false)
		if err != nil {
			return nil, err
		}
		out.Columns(cols...).Select(sel)
	case len(rows) > 0:
		if err := i.values(out, rows); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("insert: no rows")
	}
	if len(i.target) > 0 {
		cols := make([]string, len(i.target))
		for n, name := range i.target {
			_, f, err := q.resolve(name)
			if err != nil {
				return nil, err
			}
			cols[n] = f.Column
		}
		out.OnConflict(cols...)
		if i.nothing {
			out.DoNothing()
		}
		for _, s := range i.sets {
			_, f, err := q.resolve(s.field)
			if err != nil {
				return nil, err
			}
			v, err := i.value(f, s.value)
			if err != nil {
				return nil, err
			}
			out.DoUpdateSet(f.Column, "?", v)
		}
	}
	if id := meta.SingleID(); id != nil {
		col := q.root + "." + id.Column
		if em.db.Dialect().Name() == dialect.SQLite {
			col = id.Column
		}
		out.Returning(col)
	}
	return out, nil
}

// insertColumns returns the fields an insert of meta writes and their
// columns (with the discriminator).
func insertColumns(meta *mapping.Entity) ([]*mapping.Field, []string) {
	var fields []*mapping.Field
	var cols []string
	for _, f := range meta.Fields {
		if f.Generated || f.ID && meta.Generator == mapping.GenIdentity {
			continue
		}
		fields = append(fields, f)
		cols = append(cols, f.Column)
	}
	if meta.Inheritance != "" {
		cols = append(cols, meta.DiscriminatorColumn)
	}
	return fields, cols
}

// batchRows is the number of rows per statement: within the bind
// parameter limit, leaving room for the arguments of conflict updates.
func (i *Insert) batchRows() int {
	_, cols := insertColumns(i.q.meta)
	limit := dialect.MaxBindParams(i.q.em.db.Dialect()) - 10*len(i.sets)
	return max(1, min(maxBatchRows, limit/max(len(cols), 1)))
}

// values adds the rows of the entities to out.
func (i *Insert) values(out *dbal.InsertQuery, rows []any) error {
	em, meta := i.q.em, i.q.meta
	fields, cols := insertColumns(meta)
	out.Columns(cols...)
	now := em.now()
	for _, ptr := range rows {
		if reflect.TypeOf(ptr) != reflect.PointerTo(meta.GoType) {
			return fmt.Errorf("insert: %s expects *%s, got %T", meta.Name, meta.Name, ptr)
		}
		if err := assignPreInsertID(meta, ptr); err != nil {
			return err
		}
		if t, ok := ptr.(mapping.CreatedToucher); ok {
			t.TouchCreated(now)
		}
		e := &entry{meta: meta, ptr: ptr}
		vals := make([]any, 0, len(cols))
		for _, f := range fields {
			v, err := em.fieldDBValue(e, f)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", meta.Name, f.Column, err)
			}
			vals = append(vals, v)
		}
		if meta.Inheritance != "" {
			vals = append(vals, meta.DiscriminatorValue)
		}
		out.Values(vals...)
	}
	return nil
}

// value renders the value of a conflict update.
func (i *Insert) value(f *mapping.Field, value any) (any, error) {
	switch e := value.(type) {
	case Cond:
		return e.build(i.q)
	case dbal.Expr:
		return e, nil
	default:
		return i.q.value(f, value)
	}
}

// SQL renders the insert for debugging (the first statement of the rows
// given to Rows).
func (i *Insert) SQL() (string, []any, error) {
	rows := i.rows[:min(len(i.rows), i.batchRows())]
	s, err := i.build(i.q.em.db, rows)
	if err != nil {
		return "", nil, err
	}
	return s.Build()
}

// Exec runs the insert and returns the number of rows written (inserted,
// or updated on a conflict).
func (i *Insert) Exec(ctx context.Context) (int, error) {
	em := i.q.em
	if em.closed {
		return 0, ErrClosed
	}
	if err := i.check(); err != nil {
		return 0, err
	}
	meta := i.q.meta
	size := i.batchRows()
	if i.sel != nil || i.seq == nil && len(i.rows) <= size {
		s, err := i.build(em.db, i.rows)
		if err != nil {
			return 0, err
		}
		return em.runBulk(ctx, meta, s, meta.SingleID())
	}
	n := 0
	var events []*EventArgs
	err := em.db.RunInTx(ctx, nil, func(ctx context.Context, tx *dbal.Tx) error {
		batch := make([]any, 0, size)
		write := func() error {
			s, err := i.build(tx, batch)
			if err != nil {
				return err
			}
			c, args, err := em.writeBulk(ctx, meta, s, meta.SingleID())
			if err != nil {
				return err
			}
			n += c
			if args != nil && em.events.Has(EventPostBulk) {
				events = append(events, args)
			}
			batch = batch[:0]
			return nil
		}
		add := func(ptr any) error {
			batch = append(batch, ptr)
			if len(batch) < size {
				return nil
			}
			return write()
		}
		for _, ptr := range i.rows {
			if err := add(ptr); err != nil {
				return err
			}
		}
		if i.seq != nil {
			for ptr := range i.seq {
				if err := add(ptr); err != nil {
					return err
				}
			}
		}
		if len(batch) > 0 {
			return write()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, args := range events {
		if err := em.bulk(ctx, args); err != nil {
			return n, err
		}
	}
	return n, nil
}

// bulkStatement is a bulk write as dbal renders it.
type bulkStatement interface {
	Exec(ctx context.Context) (sql.Result, error)
	Rows(ctx context.Context) (*sql.Rows, error)
}

// runBulk runs s, returning the identifiers id when not nil, and raises
// EventPostBulk when rows changed.
func (em *EntityManager) runBulk(ctx context.Context, meta *mapping.Entity, s bulkStatement, id *mapping.Field) (int, error) {
	n, args, err := em.writeBulk(ctx, meta, s, id)
	if err != nil || args == nil {
		return n, err
	}
	return n, em.bulk(ctx, args)
}

// writeBulk runs s and returns the number of rows and the arguments of
// EventPostBulk (nil when no row changed).
func (em *EntityManager) writeBulk(ctx context.Context, meta *mapping.Entity, s bulkStatement, id *mapping.Field) (int, *EventArgs, error) {
	if id == nil {
		res, err := s.Exec(ctx)
		if err != nil {
			return 0, nil, err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return 0, nil, err
		}
		return int(n), &EventArgs{EM: em, Meta: meta}, nil
	}
	rows, err := s.Rows(ctx)
	if err != nil {
		return 0, nil, err
	}
	ids, err := scanIDs(rows, id, em.db.Dialect().Name())
	if err != nil || len(ids) == 0 {
		return 0, nil, err
	}
	return len(ids), &EventArgs{EM: em, Meta: meta, IDs: ids}, nil
}

// Delete removes many rows of an entity type in one statement, using field
// names. Like Update it bypasses the unit of work: loaded entities stay
// managed, cascades and hooks do not run (the database's ON DELETE rules
// do). Listeners get EventPostBulk with the IDs of the deleted rows. Build
// it with EntityManager.NewDelete.
type Delete struct{ q *Query }

// NewDelete starts a bulk delete of the entity type t (alias "t0").
func (em *EntityManager) NewDelete(t reflect.Type) *Delete {
	q := em.NewQuery(t)
	if q.err == nil && q.meta.Inheritance == mapping.Joined {
		q.fail(fmt.Errorf("delete: %s uses joined inheritance", q.meta.Name))
	}
	return &Delete{q: q}
}

// Where adds conditions joined with AND; a delete needs one.
func (d *Delete) Where(conds ...Cond) *Delete { d.q.Where(conds...); return d }

// WithoutFilter disables the named filters for this delete.
func (d *Delete) WithoutFilter(names ...string) *Delete { d.q.WithoutFilter(names...); return d }

func (d *Delete) build() (*dbal.DeleteQuery, error) {
	q := d.q
	if q.err != nil {
		return nil, q.err
	}
	if len(q.where) == 0 {
		return nil, errors.New("delete: no condition (Where)")
	}
	meta := q.meta
	out := q.em.db.NewDelete().TableAs(meta.Table, q.root)
	where, err := And(q.where...).build(q)
	if err != nil {
		return nil, err
	}
	out.WhereExpr(where)
	for _, c := range append(q.em.filterCondsExcept(meta, q.root, q.without), classConds(meta, q.root, !meta.IsPolymorphic())...) {
		out.WhereExpr(c)
	}
	if id := d.returning(); id != nil {
		col := q.root + "." + id.Column
		if q.em.db.Dialect().Name() == dialect.SQLite {
			col = id.Column
		}
		out.Returning(col)
	}
	return out, nil
}

func (d *Delete) returning() *mapping.Field {
	if !d.q.em.db.Dialect().Features().Has(dialect.FeatureReturning) {
		return nil
	}
	return d.q.meta.SingleID()
}

// SQL renders the delete for debugging.
func (d *Delete) SQL() (string, []any, error) {
	s, err := d.build()
	if err != nil {
		return "", nil, err
	}
	return s.Build()
}

// Exec runs the delete and returns the number of deleted rows.
func (d *Delete) Exec(ctx context.Context) (int, error) {
	em := d.q.em
	if em.closed {
		return 0, ErrClosed
	}
	s, err := d.build()
	if err != nil {
		return 0, err
	}
	return em.runBulk(ctx, d.q.meta, s, d.returning())
}
