package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/smatify/smorm/cache"
	"github.com/smatify/smorm/mapping"
)

// WithSecondLevelCache shares entity data of cacheable entities (tag option
// cache) between entity managers. Writes by other processes are not seen
// until entries expire; use cacheTTL for data changed elsewhere.
func WithSecondLevelCache(c cache.Cache) Option { return func(em *EntityManager) { em.cache = c } }

func cacheKey(meta *mapping.Entity, key any) string {
	return meta.Root.Table + ":" + fmt.Sprint(key)
}

func (em *EntityManager) cacheable(meta *mapping.Entity) bool {
	return em.cache != nil && meta.Cacheable
}

// cachePut stores the snapshot of e (plus its discriminator value).
func (em *EntityManager) cachePut(e *entry) {
	if !em.cacheable(e.meta) || e.key == nil || e.snapshot == nil {
		return
	}
	vals := make([]any, len(e.snapshot)+1)
	for i, v := range e.snapshot {
		if isNilValue(v) {
			v = nil // typed nil pointers become NULL
		}
		vals[i] = cloneValue(v)
	}
	vals[len(vals)-1] = e.meta.DiscriminatorValue
	em.cache.Set(cacheKey(e.meta, e.key), vals, e.meta.CacheTTL)
}

func (em *EntityManager) cacheEvict(e *entry) {
	if em.cacheable(e.meta) && e.key != nil {
		em.cache.Delete(cacheKey(e.meta, e.key))
	}
}

// cacheGet hydrates meta with key from the cache. It returns ok=false on a
// miss or when an enabled filter could hide the cached row.
func (em *EntityManager) cacheGet(ctx context.Context, meta *mapping.Entity, key any, force *entry) (any, bool, error) {
	if !em.cacheable(meta) {
		return nil, false, nil
	}
	vals, ok := em.cache.Get(cacheKey(meta, key))
	if !ok || len(vals) == 0 {
		return nil, false, nil
	}
	class := meta
	if disc, _ := vals[len(vals)-1].(string); meta.Inheritance != "" {
		if class = meta.ClassForDiscriminator(disc); class == nil || !isA(class, meta) {
			return nil, false, nil
		}
	}
	vals = vals[:len(vals)-1]
	if len(vals) != len(class.Fields) || (force != nil && force.meta != class) || !em.cachedVisible(class, vals) {
		return nil, false, nil
	}
	ptr, err := em.newHydrator(class).hydrateCached(ctx, vals, force)
	if err != nil {
		return nil, false, err
	}
	if e, ok := entryFromPtr(ptr); ok && class != meta {
		return view(e, meta), true, nil
	}
	return ptr, true, nil
}

func (h *hydrator) hydrateCached(ctx context.Context, vals []any, force *entry) (any, error) {
	ptr, err := h.values(ctx, vals, force)
	if err != nil {
		return nil, err
	}
	if err := h.em.resolvePending(ctx, h.pending); err != nil {
		return nil, err
	}
	return ptr, h.em.preloadEager(ctx, h.meta, []any{ptr})
}

// cachedVisible reports whether enabled filters allow serving vals: only
// the soft delete filter can be evaluated on cached data; any other filter
// that applies to the class bypasses the cache.
func (em *EntityManager) cachedVisible(meta *mapping.Entity, vals []any) bool {
	for _, n := range em.filters.names {
		if _, applies := em.filters.filters[n].Condition(meta, colIdent); !applies {
			continue
		}
		if n != SoftDeleteFilterName {
			return false
		}
		sd := meta.Accessor().New().(SoftDeletable)
		f := meta.FieldByColumn(sd.DeletedColumn())
		if f == nil || vals[f.Pos] != nil {
			return false
		}
	}
	return true
}

// assign stores a cached database value into a scan destination.
func assign(dest, src any) error {
	if s, ok := dest.(sql.Scanner); ok {
		return s.Scan(src)
	}
	return assignValue(reflect.ValueOf(dest), src)
}

func assignValue(dest reflect.Value, src any) error {
	if dest.Kind() != reflect.Pointer || dest.IsNil() {
		return errors.New("destination must be a non-nil pointer")
	}
	elem := dest.Elem()
	if src == nil {
		elem.SetZero()
		return nil
	}
	if elem.Kind() == reflect.Pointer {
		n := reflect.New(elem.Type().Elem())
		if err := assignValue(n, src); err != nil {
			return err
		}
		elem.Set(n)
		return nil
	}
	if b, ok := src.([]byte); ok && elem.Kind() != reflect.Slice && elem.Kind() != reflect.String {
		// Text protocol values (e.g. MySQL without bind arguments).
		return assignText(elem, string(b))
	}
	sv := reflect.ValueOf(src)
	switch {
	case sv.Type().AssignableTo(elem.Type()):
		elem.Set(sv)
	case elem.Kind() == reflect.Bool && sv.CanInt():
		elem.SetBool(sv.Int() != 0)
	case sv.Type().ConvertibleTo(elem.Type()) && (sv.Kind() != reflect.String || elem.Kind() == reflect.String || elem.Kind() == reflect.Slice):
		elem.Set(sv.Convert(elem.Type()))
	case sv.Kind() == reflect.String && elem.Type() == reflect.TypeFor[time.Time]():
		t, err := time.Parse(time.RFC3339Nano, sv.String())
		if err != nil {
			return err
		}
		elem.Set(reflect.ValueOf(t))
	default:
		return fmt.Errorf("cannot assign %T to %s", src, elem.Type())
	}
	return nil
}

func assignText(elem reflect.Value, s string) error {
	switch {
	case elem.CanInt():
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		elem.SetInt(n)
	case elem.CanUint():
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return err
		}
		elem.SetUint(n)
	case elem.CanFloat():
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		elem.SetFloat(f)
	case elem.Kind() == reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		elem.SetBool(b)
	default:
		return assignValue(elem.Addr(), s)
	}
	return nil
}
