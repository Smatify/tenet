// Package types maps between Go values, database values and SQL column types.
//
// Each Type knows its column declaration per dialect, how to convert a Go value
// for the driver and how to scan a database value back into a Go value. A
// Registry holds the types by name and infers the type of a Go struct field.
package types

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"sync"

	"github.com/smatify/tenet/dialect"
)

// Name identifies a type in mapping tags, e.g. `type:json`.
type Name string

// Built-in type names.
const (
	String     Name = "string"
	Text       Name = "text"
	SmallInt   Name = "smallint"
	Integer    Name = "integer"
	BigInt     Name = "bigint"
	Boolean    Name = "boolean"
	Decimal    Name = "decimal"
	Float      Name = "float"
	Date       Name = "date"
	Time       Name = "time"
	DateTime   Name = "datetime"
	DateTimeTZ Name = "datetimetz"
	JSON       Name = "json"
	UUID       Name = "uuid"
	Binary     Name = "binary"
	Blob       Name = "blob"
)

// Options are the column attributes that influence the SQL declaration.
type Options struct {
	Length    int  // for string/binary; 0 means default
	Precision int  // for decimal (total digits) and fractional seconds
	Scale     int  // for decimal
	Fixed     bool // CHAR/BINARY instead of VARCHAR/VARBINARY
}

// Type converts values between Go and the database.
type Type interface {
	// Name returns the registry name.
	Name() Name
	// SQLDeclaration returns the column type for DDL.
	SQLDeclaration(d dialect.Name, o Options) string
	// ToDatabase converts a Go value into a value the driver accepts.
	ToDatabase(v any, d dialect.Name) (any, error)
	// ScanTarget returns the destination to pass to rows.Scan so that the
	// result ends up in dest, which is a non-nil pointer to the Go field.
	ScanTarget(dest any, d dialect.Name) any
}

// Base implements pass-through conversion. Types embed it and implement
// Name and SQLDeclaration.
type Base struct{}

// ToDatabase returns v unchanged.
func (Base) ToDatabase(v any, _ dialect.Name) (any, error) { return v, nil }

// ScanTarget returns dest unchanged.
func (Base) ScanTarget(dest any, _ dialect.Name) any { return dest }

// Registry holds types by name and Go type bindings.
type Registry struct {
	mu     sync.RWMutex
	byName map[Name]Type
	byGo   map[reflect.Type]Name
}

// NewRegistry returns a registry containing all built-in types.
func NewRegistry() *Registry {
	r := &Registry{byName: map[Name]Type{}, byGo: map[reflect.Type]Name{}}
	for _, t := range builtins() {
		r.Register(t)
	}
	for goType, name := range defaultBindings() {
		r.Bind(goType, name)
	}
	return r
}

// Default is the process wide registry.
var Default = NewRegistry()

// Register adds or replaces a type.
func (r *Registry) Register(t Type) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName[t.Name()] = t
}

// Bind makes Infer return the named type for goType (pointer types are
// dereferenced before lookup).
func (r *Registry) Bind(goType reflect.Type, name Name) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byGo[goType] = name
}

// Lookup returns the type registered under name.
func (r *Registry) Lookup(name Name) (Type, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byName[name]
	return t, ok
}

// MustLookup is like Lookup but panics for unknown names.
func (r *Registry) MustLookup(name Name) Type {
	t, ok := r.Lookup(name)
	if !ok {
		panic(fmt.Sprintf("types: unknown type %q", name))
	}
	return t
}

var (
	scannerType = reflect.TypeFor[sql.Scanner]()
	valuerType  = reflect.TypeFor[driver.Valuer]()
)

// Infer returns the default type for a Go type. Explicit bindings win over
// kind based rules. It returns false when the type must be given explicitly.
func (r *Registry) Infer(goType reflect.Type) (Type, bool) {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	r.mu.RLock()
	name, ok := r.byGo[goType]
	r.mu.RUnlock()
	if ok {
		return r.Lookup(name)
	}
	if name, ok := inferKind(goType); ok {
		return r.Lookup(name)
	}
	return nil, false
}

func inferKind(t reflect.Type) (Name, bool) {
	switch t.Kind() {
	case reflect.String:
		return String, true
	case reflect.Bool:
		return Boolean, true
	case reflect.Int8, reflect.Uint8, reflect.Int16, reflect.Uint16:
		return SmallInt, true
	case reflect.Int32, reflect.Uint32:
		return Integer, true
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64:
		return BigInt, true
	case reflect.Float32, reflect.Float64:
		return Float, true
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return Binary, true
		}
	case reflect.Map:
		if t.Key().Kind() == reflect.String {
			return JSON, true
		}
	}
	return "", false
}

// IsValuerScanner reports whether t (or *t) handles its own SQL conversion.
func IsValuerScanner(t reflect.Type) bool {
	return reflect.PointerTo(t).Implements(scannerType) && (t.Implements(valuerType) || reflect.PointerTo(t).Implements(valuerType))
}
