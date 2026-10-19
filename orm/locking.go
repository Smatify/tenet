package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/internal/brand"
	"github.com/smatify/smorm/mapping"
)

// ErrOptimisticLock is matched by OptimisticLockError.
var ErrOptimisticLock = errors.New(brand.Name + ": optimistic lock failed")

// OptimisticLockError reports that an entity was changed by someone else
// since it was loaded (its version no longer matches).
type OptimisticLockError struct {
	Entity  string
	ID      any
	Version any
}

func (e *OptimisticLockError) Error() string {
	return fmt.Sprintf("%s: the %s(%v) was changed concurrently (expected version %v)", brand.Name, e.Entity, e.ID, e.Version)
}

// Is makes errors.Is(err, ErrOptimisticLock) work.
func (e *OptimisticLockError) Is(target error) bool { return target == ErrOptimisticLock }

// LockMode selects how Lock and FindLocked lock an entity.
type LockMode int

// Lock modes.
const (
	LockOptimistic       LockMode = iota + 1 // compare the version only
	LockPessimisticWrite                     // SELECT ... FOR UPDATE
	LockPessimisticRead                      // SELECT ... FOR SHARE
)

// nextVersion returns the version value following cur.
func nextVersion(f *mapping.Field, cur any, now time.Time) any {
	rv := reflect.ValueOf(cur)
	if f.GoType == reflect.TypeFor[time.Time]() {
		return now.UTC().Truncate(time.Microsecond)
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(rv.Int() + 1).Convert(f.GoType).Interface()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(rv.Uint() + 1).Convert(f.GoType).Interface()
	}
	return cur
}

// initVersion sets the starting version of a new entity.
func (em *EntityManager) initVersion(e *entry) {
	v := e.meta.Version
	if v == nil {
		return
	}
	acc := e.meta.Accessor()
	if cur := acc.Get(e.ptr, v.Pos); reflect.ValueOf(cur).IsZero() {
		acc.Set(e.ptr, v.Pos, nextVersion(v, cur, em.now()))
	}
}

// bumpVersion advances the version of a changed entity.
func (em *EntityManager) bumpVersion(e *entry) {
	v := e.meta.Version
	acc := e.meta.Accessor()
	acc.Set(e.ptr, v.Pos, nextVersion(v, acc.Get(e.ptr, v.Pos), em.now()))
}

// Lock locks a managed entity. LockOptimistic checks that its version equals
// version; the pessimistic modes need a transaction (see Transactional) and
// issue SELECT ... FOR UPDATE / FOR SHARE.
func (em *EntityManager) Lock(ctx context.Context, ptr any, mode LockMode, version any) error {
	meta, err := em.meta(ptr)
	if err != nil {
		return err
	}
	e := em.entryOf(meta, ptr)
	if e == nil || e.state != StateManaged {
		return fmt.Errorf("%s: Lock requires a managed entity", meta.Name)
	}
	switch mode {
	case LockOptimistic:
		if meta.Version == nil {
			return fmt.Errorf("%s: optimistic locking needs a version field", meta.Name)
		}
		if err := em.initialize(ctx, e); err != nil && !e.initialized {
			return err
		}
		cur := meta.Accessor().Get(ptr, meta.Version.Pos)
		if !equalDB(normalizeKey(cur), normalizeKey(version)) {
			if t, ok := cur.(time.Time); !ok || !t.Equal(asTime(version)) {
				return &OptimisticLockError{Entity: meta.Name, ID: e.key, Version: version}
			}
		}
		return nil
	case LockPessimisticWrite, LockPessimisticRead:
		if _, inTx := em.db.(*dbal.Tx); !inTx {
			return fmt.Errorf("%s: pessimistic locks need a transaction", meta.Name)
		}
		q := em.db.NewSelect().Table(meta.Table)
		for _, f := range meta.ID {
			q.Column(f.Column)
		}
		whereID(q, meta, "", idValues(meta, ptr))
		q.Lock(dbalLock(mode))
		rows, err := q.Rows(ctx)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			return ErrNotFound
		}
		return rows.Err()
	}
	return fmt.Errorf("unknown lock mode %d", mode)
}

func asTime(v any) time.Time {
	t, _ := v.(time.Time)
	return t
}

func dbalLock(mode LockMode) dbal.LockMode {
	if mode == LockPessimisticRead {
		return dbal.LockForShare
	}
	return dbal.LockForUpdate
}

// FindLocked loads an entity with a pessimistic lock, refreshing it if it is
// already managed. It needs a transaction.
func (em *EntityManager) FindLocked(ctx context.Context, t reflect.Type, mode LockMode, id ...any) (any, error) {
	if em.closed {
		return nil, ErrClosed
	}
	if mode != LockPessimisticWrite && mode != LockPessimisticRead {
		return nil, errors.New("FindLocked needs a pessimistic lock mode")
	}
	if _, inTx := em.db.(*dbal.Tx); !inTx {
		return nil, errors.New("pessimistic locks need a transaction")
	}
	meta, err := em.reg.Get(t)
	if err != nil {
		return nil, err
	}
	if len(id) != len(meta.ID) {
		return nil, fmt.Errorf("%s: FindLocked needs %d identifier values, got %d", meta.Name, len(meta.ID), len(id))
	}
	q := em.selectEntity(meta, "")
	whereID(q, meta, "", id)
	q.Lock(dbalLock(mode))
	force := em.lookup(meta, idKey(id))
	res, err := em.loadQuery(ctx, meta, q, force)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, ErrNotFound
	}
	return res[0], nil
}
