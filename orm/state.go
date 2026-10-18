// Package orm implements the entity manager and unit of work: identity map,
// change tracking, commit ordering and hydration. Applications use it
// through the root smorm package.
package orm

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/smatify/smorm/internal/brand"
	"github.com/smatify/smorm/mapping"
)

// State is the lifecycle state of an entity relative to an entity manager.
type State int

// Entity states.
const (
	StateNew      State = iota // not known to the entity manager
	StateManaged               // tracked; changes are written on flush
	StateRemoved               // scheduled for deletion
	StateDetached              // was managed, no longer tracked
)

func (s State) String() string {
	return [...]string{"new", "managed", "removed", "detached"}[s]
}

var (
	// ErrNotFound is returned when an entity does not exist.
	ErrNotFound = errors.New(brand.Name + ": entity not found")
	// ErrClosed is returned after a failed flush closed the entity manager.
	ErrClosed = errors.New(brand.Name + ": the entity manager is closed after a failed flush; create a new one")
	// ErrNotEntity is returned for values that are not pointers to entities.
	ErrNotEntity = errors.New(brand.Name + ": value is not a pointer to an entity")
)

// NewEntityFoundError reports a new entity reachable through an association
// that does not cascade persist (Doctrine: "A new entity was found through
// the relationship ...").
type NewEntityFoundError struct {
	Source, Assoc, Target string
}

func (e *NewEntityFoundError) Error() string {
	return fmt.Sprintf("%s: a new entity was found through the relationship %s.%s that was not configured to cascade persist; "+
		"persist the %s explicitly or add cascade:persist", brand.Name, e.Source, e.Assoc, e.Target)
}

// entry is the per-instance ORM state stored in the entity header.
type entry struct {
	em          *EntityManager
	meta        *mapping.Entity
	ptr         any // pointer to the entity struct
	state       State
	key         any   // identity map key; nil while the ID is unknown
	snapshot    []any // database values by field position at load/flush time
	initialized bool  // false for references (proxies) not loaded yet
	deferred    []*mapping.Field
}

// idKey builds an identity map key from identifier values.
func idKey(vals []any) any {
	if len(vals) == 1 {
		return normalizeKey(vals[0])
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = fmt.Sprintf("%v", normalizeKey(v))
	}
	return strings.Join(parts, "\x00")
}

// normalizeKey makes equal identifiers of different integer types and
// pointers to them produce the same key.
func normalizeKey(v any) any {
	rv := reflect.ValueOf(v)
	for rv.IsValid() && rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint())
	case reflect.String:
		return rv.String()
	}
	return rv.Interface()
}

func isZeroID(v any) bool {
	rv := reflect.ValueOf(v)
	return !rv.IsValid() || rv.IsZero()
}

var brandName = brand.Name

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }
