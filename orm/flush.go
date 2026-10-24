package orm

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/uuid"
)

// fieldDBValue returns the database value of field f of e, resolving join
// columns through the association.
func (em *EntityManager) fieldDBValue(e *entry, f *mapping.Field) (any, error) {
	d := em.db.Dialect().Name()
	if f.Assoc != nil {
		if e.deferred != nil {
			for _, df := range e.deferred {
				if df == f {
					return nil, nil
				}
			}
		}
		target := em.assocTarget(e, f.Assoc)
		if target == nil {
			return nil, nil
		}
		tm := f.Assoc.Target
		ref := tm.FieldByColumn(f.Assoc.JoinColumns[0].Referenced)
		v := tm.Accessor().Get(target, ref.Pos)
		if isZeroID(v) {
			return nil, nil
		}
		return ref.Type.ToDatabase(v, d)
	}
	v := e.meta.Accessor().Get(e.ptr, f.Pos)
	if f.NullZero && reflect.ValueOf(v).IsZero() {
		return nil, nil
	}
	return f.Type.ToDatabase(v, d)
}

// assocTarget returns the entity a to-one association points to, or nil.
func (em *EntityManager) assocTarget(e *entry, a *mapping.Association) any {
	rel := related(e.meta, e.ptr, a)
	if len(rel) == 0 {
		return nil
	}
	return rel[0]
}

func (em *EntityManager) dbValues(e *entry) ([]any, error) {
	out := make([]any, len(e.meta.Fields))
	for i, f := range e.meta.Fields {
		v, err := em.fieldDBValue(e, f)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", e.meta.Name, f.Column, err)
		}
		out[i] = cloneValue(v)
	}
	return out, nil
}

func (em *EntityManager) takeSnapshot(e *entry) error {
	vals, err := em.dbValues(e)
	if err != nil {
		return err
	}
	e.snapshot = vals
	return nil
}

func cloneValue(v any) any {
	if b, ok := v.([]byte); ok {
		return bytes.Clone(b)
	}
	return v
}

func equalDB(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch x := a.(type) {
	case []byte:
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	if ta.Comparable() {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

// changes returns the fields whose database value differs from the snapshot.
func (em *EntityManager) changes(e *entry) ([]*mapping.Field, []any, error) {
	vals, err := em.dbValues(e)
	if err != nil {
		return nil, nil, err
	}
	var changed []*mapping.Field
	for i, f := range e.meta.Fields {
		if f.ID {
			continue
		}
		if !equalDB(e.snapshot[i], vals[i]) {
			changed = append(changed, f)
		}
	}
	return changed, vals, nil
}

// assignPreInsertID generates identifiers that are known before insertion.
func assignPreInsertID(meta *mapping.Entity, ptr any) error {
	id := meta.SingleID()
	if id == nil || !isZeroID(meta.Accessor().Get(ptr, id.Pos)) {
		return nil
	}
	switch meta.Generator {
	case mapping.GenUUIDv7:
		meta.Accessor().Set(ptr, id.Pos, convertTo(uuid.NewV7(), id.GoType))
	case mapping.GenUUIDv4:
		meta.Accessor().Set(ptr, id.Pos, convertTo(uuid.NewV4(), id.GoType))
	}
	return nil
}

// discoverNew finds new entities reachable from tracked entities. Entities
// reachable through cascade:persist are persisted; others are an error.
func (em *EntityManager) discoverNew() error {
	seen := map[any]bool{}
	for i := 0; i < len(em.tracked); i++ { // tracked may grow
		e := em.tracked[i]
		if !e.initialized || (e.state != StateManaged && e.state != StateNew) {
			continue
		}
		for _, a := range e.meta.Assocs {
			for _, p := range related(e.meta, e.ptr, a) {
				if c := em.entryOf(a.Target, p); c != nil && c.state != StateDetached {
					continue
				}
				if a.Cascade.Has(mapping.CascadePersist) {
					if err := em.persist(a.Target, p, seen); err != nil {
						return err
					}
					continue
				}
				if a.Owning && a.Kind.IsToOne() {
					return &NewEntityFoundError{Source: e.meta.Name, Assoc: a.Name, Target: a.Target.Name}
				}
			}
		}
	}
	return nil
}

type update struct {
	e      *entry
	fields []*mapping.Field
	cs     *ChangeSet
	soft   bool // soft delete: the entity is forgotten after the flush
}

// Flush writes all pending inserts, updates and deletes in one transaction.
// If it fails, the transaction is rolled back and the entity manager is
// closed, like Doctrine's.
func (em *EntityManager) Flush(ctx context.Context) error {
	if em.closed {
		return ErrClosed
	}
	em.compact()
	if err := em.scheduleOrphans(); err != nil {
		return err
	}
	if err := em.discoverNew(); err != nil {
		return err
	}
	var inserts, removes []*entry
	var updates []update
	now := em.now()
	for _, e := range em.tracked {
		if e.state == StateNew {
			if err := em.lifecycle(ctx, EventPrePersist, e, nil); err != nil {
				return err
			}
		}
	}
	for _, e := range em.tracked {
		switch e.state {
		case StateNew:
			inserts = append(inserts, e)
		case StateRemoved:
			if err := em.lifecycle(ctx, EventPreRemove, e, nil); err != nil {
				return err
			}
			if sd, ok := e.ptr.(SoftDeletable); ok {
				if !e.initialized {
					if err := em.initialize(ctx, e); err != nil {
						return err
					}
				}
				if !sd.IsDeleted() {
					sd.MarkDeleted(now)
					changed, vals, err := em.changes(e)
					if err != nil {
						return err
					}
					updates = append(updates, update{e: e, fields: changed, cs: newChangeSet(e, changed, vals), soft: true})
					continue
				}
			}
			removes = append(removes, e)
		case StateManaged:
			if !e.initialized {
				continue
			}
			u, err := em.prepareUpdate(ctx, e, now)
			if err != nil {
				return err
			}
			if u != nil {
				updates = append(updates, *u)
			}
		}
	}
	joins := em.joinTableChanges()
	if len(inserts)+len(updates)+len(removes)+len(joins) == 0 {
		return nil
	}
	if err := em.events.dispatch(ctx, EventOnFlush, &EventArgs{EM: em}); err != nil {
		return err
	}
	ordered, err := commitOrder(em, inserts)
	if err != nil {
		return err
	}
	deleteOrder, nullify, err := deleteOrder(em, removes)
	if err != nil {
		return err
	}
	for _, e := range ordered {
		if t, ok := e.ptr.(mapping.CreatedToucher); ok {
			t.TouchCreated(now)
		}
		em.initVersion(e)
	}
	run := func(ctx context.Context, db dbal.IDB) error {
		for i := 0; i < len(ordered); {
			batch := batchFrom(ordered, i)
			if len(batch) > 1 {
				if err := em.insertBatch(ctx, db, batch); err != nil {
					return err
				}
			} else if err := em.insert(ctx, db, ordered[i]); err != nil {
				return err
			}
			for _, e := range batch {
				if e.key == nil {
					em.register(e, idKey(idValues(e.meta, e.ptr)))
				}
				if err := em.lifecycle(ctx, EventPostPersist, e, nil); err != nil {
					return err
				}
			}
			i += len(batch)
		}
		for _, e := range ordered {
			if len(e.deferred) > 0 {
				fields := e.deferred
				e.deferred = nil
				if err := em.update(ctx, db, e, fields); err != nil {
					return err
				}
			}
		}
		for _, u := range updates {
			if err := em.update(ctx, db, u.e, u.fields); err != nil {
				return err
			}
			ev := EventPostUpdate
			if u.soft {
				ev = EventPostRemove
			}
			if err := em.lifecycle(ctx, ev, u.e, u.cs); err != nil {
				return err
			}
		}
		for _, r := range joins {
			target := em.entryOf(targetMeta(r), r.target)
			if target == nil || target.state == StateRemoved {
				continue
			}
			if err := em.writeJoinRow(ctx, db, r, target.meta); err != nil {
				return wrapErr(r.owner.meta, "join table", err)
			}
		}
		for _, n := range nullify {
			if err := em.nullify(ctx, db, n.e, n.f); err != nil {
				return err
			}
		}
		for _, e := range deleteOrder {
			if err := em.delete(ctx, db, e); err != nil {
				return err
			}
			if err := em.lifecycle(ctx, EventPostRemove, e, nil); err != nil {
				return err
			}
		}
		return nil
	}
	if _, inTx := em.db.(*dbal.Tx); inTx {
		err = run(ctx, em.db)
	} else {
		err = em.db.RunInTx(ctx, nil, func(ctx context.Context, tx *dbal.Tx) error { return run(ctx, tx) })
	}
	if err != nil {
		em.closed = true
		return err
	}
	for _, e := range ordered {
		e.state = StateManaged
		em.adoptCollections(e)
		if err := em.takeSnapshot(e); err != nil {
			return err
		}
		em.cachePut(e)
	}
	em.syncCollectionSnapshots()
	for _, u := range updates {
		if u.soft {
			em.cacheEvict(u.e)
			em.forget(u.e)
			continue
		}
		if err := em.takeSnapshot(u.e); err != nil {
			return err
		}
		em.cachePut(u.e)
	}
	for _, e := range deleteOrder {
		em.cacheEvict(e)
		em.forget(e)
	}
	em.compact()
	return em.events.dispatch(ctx, EventPostFlush, &EventArgs{EM: em})
}

// prepareUpdate computes the change set of a managed entity and runs the
// update hooks. It returns nil when nothing changed.
func (em *EntityManager) prepareUpdate(ctx context.Context, e *entry, now time.Time) (*update, error) {
	changed, vals, err := em.changes(e)
	if err != nil || len(changed) == 0 {
		return nil, err
	}
	if err := em.lifecycle(ctx, EventPreUpdate, e, newChangeSet(e, changed, vals)); err != nil {
		return nil, err
	}
	if t, ok := e.ptr.(mapping.UpdatedToucher); ok {
		t.TouchUpdated(now)
	}
	if changed, vals, err = em.changes(e); err != nil || len(changed) == 0 {
		return nil, err
	}
	if v := e.meta.Version; v != nil && !(len(changed) == 1 && changed[0] == v) {
		em.bumpVersion(e)
		if changed, vals, err = em.changes(e); err != nil {
			return nil, err
		}
	}
	return &update{e: e, fields: changed, cs: newChangeSet(e, changed, vals)}, nil
}

// compact drops detached entries from the tracked list.
func (em *EntityManager) compact() {
	out := em.tracked[:0]
	for _, e := range em.tracked {
		if e.state != StateDetached {
			out = append(out, e)
		}
	}
	clear(em.tracked[len(out):])
	em.tracked = out
}

func (em *EntityManager) insert(ctx context.Context, db dbal.IDB, e *entry) error {
	meta := e.meta
	d := db.Dialect()
	acc := meta.Accessor()
	idf := meta.SingleID()
	if meta.Generator == mapping.GenSequence && idf != nil && isZeroID(acc.Get(e.ptr, idf.Pos)) {
		var next int64
		if err := db.QueryRowContext(ctx, nextValSQL(d, meta.Sequence)).Scan(&next); err != nil {
			return fmt.Errorf("%s: sequence %s: %w", meta.Name, meta.Sequence, err)
		}
		acc.Set(e.ptr, idf.Pos, convertTo(next, idf.GoType))
	}
	identity := meta.Generator == mapping.GenIdentity && idf != nil
	tables := meta.Tables()
	for i, tbl := range tables {
		q := db.NewInsert().Table(tbl)
		var cols []string
		var vals []any
		var generated []*mapping.Field // read back after the insert
		if identity && i == 0 {
			generated = append(generated, idf)
		}
		for _, f := range meta.Fields {
			if f.Table != tbl && !(i > 0 && f.ID) {
				continue
			}
			if identity && f.ID && i == 0 {
				continue
			}
			if f.Generated && f.Table == tbl && isZeroID(acc.Get(e.ptr, f.Pos)) {
				generated = append(generated, f)
				continue
			}
			v, err := em.fieldDBValue(e, f)
			if err != nil {
				return err
			}
			cols = append(cols, f.Column)
			vals = append(vals, v)
		}
		if i == 0 && meta.Inheritance != "" {
			cols = append(cols, meta.DiscriminatorColumn)
			vals = append(vals, meta.DiscriminatorValue)
		}
		if len(generated) == 0 {
			if len(cols) == 0 {
				continue
			}
			q.Columns(cols...).Values(vals...)
			if _, err := q.Exec(ctx); err != nil {
				return wrapErr(meta, "insert", err)
			}
			continue
		}
		if len(cols) == 0 {
			// Only an identity column: INSERT ... DEFAULT VALUES.
			if err := em.insertDefaults(ctx, db, e); err != nil {
				return err
			}
			continue
		}
		q.Columns(cols...).Values(vals...)
		if err := em.insertGenerated(ctx, db, e, q, generated); err != nil {
			return err
		}
	}
	return nil
}

// insertGenerated executes q and stores the values the database generated
// for fields (the identity identifier or generated:identity fields; a table
// has at most one identity column).
func (em *EntityManager) insertGenerated(ctx context.Context, db dbal.IDB, e *entry, q *dbal.InsertQuery, fields []*mapping.Field) error {
	meta, d, acc := e.meta, db.Dialect(), e.meta.Accessor()
	if d.Features().Has(dialect.FeatureInsertReturning) || d.Features().Has(dialect.FeatureOutput) {
		cols := make([]string, len(fields))
		targets := make([]any, len(fields))
		for i, f := range fields {
			cols[i] = f.Column
			targets[i] = f.Type.ScanTarget(acc.Addr(e.ptr, f.Pos), d.Name())
		}
		return wrapErr(meta, "insert", q.Returning(cols...).Scan(ctx, targets...))
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return wrapErr(meta, "insert", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return wrapErr(meta, "insert", err)
	}
	acc.Set(e.ptr, fields[0].Pos, convertTo(id, fields[0].GoType))
	return nil
}

func (em *EntityManager) insertDefaults(ctx context.Context, db dbal.IDB, e *entry) error {
	meta := e.meta
	d := db.Dialect()
	idf := meta.SingleID()
	tbl := d.QuoteIdent(meta.Tables()[0])
	col := d.QuoteIdent(idf.Column)
	acc := meta.Accessor()
	target := idf.Type.ScanTarget(acc.Addr(e.ptr, idf.Pos), d.Name())
	var err error
	switch {
	case d.Name() == dialect.MSSQL:
		err = db.QueryRowContext(ctx, "INSERT INTO "+tbl+" OUTPUT INSERTED."+col+" DEFAULT VALUES").Scan(target)
	case d.Features().Has(dialect.FeatureInsertReturning) && d.Name() != dialect.MariaDB:
		err = db.QueryRowContext(ctx, "INSERT INTO "+tbl+" DEFAULT VALUES RETURNING "+col).Scan(target)
	default:
		var res interface{ LastInsertId() (int64, error) }
		res, err = db.ExecContext(ctx, "INSERT INTO "+tbl+" () VALUES ()")
		if err == nil {
			var id int64
			if id, err = res.LastInsertId(); err == nil {
				acc.Set(e.ptr, idf.Pos, convertTo(id, idf.GoType))
			}
		}
	}
	return wrapErr(meta, "insert", err)
}

func nextValSQL(d dialect.Dialect, seq string) string {
	q := d.QuoteIdent(seq)
	switch d.Name() {
	case dialect.Postgres:
		return "SELECT nextval(" + d.QuoteString(q) + ")"
	case dialect.MSSQL:
		return "SELECT NEXT VALUE FOR " + q
	default:
		return "SELECT NEXTVAL(" + q + ")"
	}
}

func (em *EntityManager) update(ctx context.Context, db dbal.IDB, e *entry, fields []*mapping.Field) error {
	if tables := e.meta.Tables(); len(tables) > 1 {
		for _, tbl := range tables {
			var part []*mapping.Field
			for _, f := range fields {
				if f.Table == tbl {
					part = append(part, f)
				}
			}
			if len(part) > 0 {
				if err := em.updateTable(ctx, db, e, tbl, part); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return em.updateTable(ctx, db, e, e.meta.Table, fields)
}

func (em *EntityManager) updateTable(ctx context.Context, db dbal.IDB, e *entry, table string, fields []*mapping.Field) error {
	q := db.NewUpdate().Table(table)
	versioned := false
	for _, f := range fields {
		v, err := em.fieldDBValue(e, f)
		if err != nil {
			return err
		}
		q.Set(f.Column, v)
		versioned = versioned || f.Version
	}
	em.whereEntity(e, func(c string, args ...any) { q.Where(c, args...) })
	var old any
	if versioned && e.snapshot != nil {
		old = e.snapshot[e.meta.Version.Pos]
		q.Where("? = ?", dbal.Ident(e.meta.Version.Column), old)
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return wrapErr(e.meta, "update", err)
	}
	if versioned && e.snapshot != nil {
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return &OptimisticLockError{Entity: e.meta.Name, ID: e.key, Version: old}
		}
	}
	return nil
}

func (em *EntityManager) nullify(ctx context.Context, db dbal.IDB, e *entry, f *mapping.Field) error {
	q := db.NewUpdate().Table(f.Table).Set(f.Column, nil)
	em.whereEntity(e, func(c string, args ...any) { q.Where(c, args...) })
	_, err := q.Exec(ctx)
	return wrapErr(e.meta, "update", err)
}

func (em *EntityManager) delete(ctx context.Context, db dbal.IDB, e *entry) error {
	// Joined subclass rows are removed by their ON DELETE CASCADE keys.
	q := db.NewDelete().Table(e.meta.Root.Table)
	em.whereEntity(e, func(c string, args ...any) { q.Where(c, args...) })
	_, err := q.Exec(ctx)
	return wrapErr(e.meta, "delete", err)
}

func (em *EntityManager) whereEntity(e *entry, where func(string, ...any)) {
	id := idValues(e.meta, e.ptr)
	for i, f := range e.meta.ID {
		where("? = ?", dbal.Ident(f.Column), idArg{f: f, v: id[i]})
	}
}

func wrapErr(meta *mapping.Entity, op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %s: %w", meta.Name, op, err)
}

func targetMeta(r joinRow) *mapping.Entity {
	for _, a := range r.owner.meta.Assocs {
		if a.JoinTable != nil && a.JoinTable.Name == r.spec.table {
			return a.Target
		}
	}
	return nil
}

// batchable reports whether e can be inserted together with other rows:
// no column is generated by the database and no foreign key is deferred.
func batchable(e *entry) bool {
	return e.meta.Generator != mapping.GenIdentity && len(e.deferred) == 0 && e.meta.Inheritance == "" &&
		!slices.ContainsFunc(e.meta.Fields, func(f *mapping.Field) bool { return f.Generated })
}

// batchFrom returns the run of consecutive batchable entries of the same
// entity type starting at i (at least ordered[i] itself).
func batchFrom(ordered []*entry, i int) []*entry {
	j := i + 1
	if batchable(ordered[i]) {
		for j < len(ordered) && ordered[j].meta == ordered[i].meta && batchable(ordered[j]) {
			j++
		}
	}
	return ordered[i:j]
}

// maxBindParams is a conservative bind parameter limit per statement
// (SQL Server allows 2100, SQLite builds commonly 32766).
const maxBindParams = 2000

// maxBatchRows caps rows per multi-row INSERT.
const maxBatchRows = 500

// insertBatch writes entities of one type with multi-row INSERT statements.
func (em *EntityManager) insertBatch(ctx context.Context, db dbal.IDB, batch []*entry) error {
	meta := batch[0].meta
	d := db.Dialect()
	acc := meta.Accessor()
	if idf := meta.SingleID(); meta.Generator == mapping.GenSequence && idf != nil {
		for _, e := range batch {
			if isZeroID(acc.Get(e.ptr, idf.Pos)) {
				var next int64
				if err := db.QueryRowContext(ctx, nextValSQL(d, meta.Sequence)).Scan(&next); err != nil {
					return fmt.Errorf("%s: sequence %s: %w", meta.Name, meta.Sequence, err)
				}
				acc.Set(e.ptr, idf.Pos, convertTo(next, idf.GoType))
			}
		}
	}
	cols := meta.Columns()
	rows := max(1, min(maxBatchRows, maxBindParams/max(len(cols), 1)))
	for start := 0; start < len(batch); start += rows {
		chunk := batch[start:min(start+rows, len(batch))]
		q := db.NewInsert().Table(meta.Table).Columns(cols...)
		for _, e := range chunk {
			vals := make([]any, len(meta.Fields))
			for i, f := range meta.Fields {
				v, err := em.fieldDBValue(e, f)
				if err != nil {
					return err
				}
				vals[i] = v
			}
			q.Values(vals...)
		}
		if _, err := q.Exec(ctx); err != nil {
			return wrapErr(meta, "insert", err)
		}
	}
	return nil
}
