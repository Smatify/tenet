package mapping

import (
	"reflect"
	"slices"

	"github.com/smatify/smorm/internal/entity"
)

// GeneratedType is registered by generated code for one entity type. Fields
// and Assocs are keyed by the dotted Go field path ("title",
// "Timestamps.createdAt"). Paths missing from the maps (for example
// unexported fields of embedded structs from other packages) fall back to
// reflection.
type GeneratedType struct {
	New    func() any
	Header func(ptr any) *entity.Header
	Fields map[string]GeneratedField
	Assocs map[string]GeneratedField
}

// GeneratedField accesses one field without reflection.
type GeneratedField struct {
	Get  func(ptr any) any
	Set  func(ptr any, v any)
	Addr func(ptr any) any
}

// RegisterGenerated installs generated accessors for the struct type t.
func RegisterGenerated(t reflect.Type, g GeneratedType) {
	generatedMu.Lock()
	defer generatedMu.Unlock()
	generatedTypes[t] = g
}

var generatedTypes = map[reflect.Type]GeneratedType{}

// FieldRef names a mapped field or association of an entity in queries.
// Generated code declares one per field, e.g. PostFields.Title.
type FieldRef struct {
	Entity string // Go type name
	Name   string // Go field name
}

// path returns the dotted Go field path of index within t.
func path(t reflect.Type, index []int) string {
	s := ""
	for i, x := range index {
		f := t.Field(x)
		if i > 0 {
			s += "."
		}
		s += f.Name
		t = f.Type
	}
	return s
}

// composedAccessor uses generated functions where available.
type composedAccessor struct {
	reflectAccessor
	g      GeneratedType
	fields []GeneratedField // by Field.Pos; zero value means reflection
	assocs []GeneratedField
}

func newComposedAccessor(e *Entity, g GeneratedType) *composedAccessor {
	a := &composedAccessor{reflectAccessor: reflectAccessor{e: e}, g: g}
	a.fields = make([]GeneratedField, len(e.Fields))
	for i, f := range e.Fields {
		if f.Index != nil {
			a.fields[i] = g.Fields[path(e.GoType, f.Index)]
		}
	}
	a.assocs = make([]GeneratedField, len(e.Assocs))
	for i, as := range e.Assocs {
		a.assocs[i] = g.Assocs[path(e.GoType, as.Index)]
	}
	return a
}

func (a *composedAccessor) New() any {
	if a.g.New != nil {
		return a.g.New()
	}
	return a.reflectAccessor.New()
}

func (a *composedAccessor) Header(ptr any) *entity.Header {
	if a.g.Header != nil {
		return a.g.Header(ptr)
	}
	return a.reflectAccessor.Header(ptr)
}

func (a *composedAccessor) Get(ptr any, i int) any {
	if g := a.fields[i].Get; g != nil {
		return g(ptr)
	}
	return a.reflectAccessor.Get(ptr, i)
}

func (a *composedAccessor) Set(ptr any, i int, v any) {
	if s := a.fields[i].Set; s != nil {
		s(ptr, v)
		return
	}
	a.reflectAccessor.Set(ptr, i, v)
}

func (a *composedAccessor) Addr(ptr any, i int) any {
	if f := a.fields[i].Addr; f != nil {
		return f(ptr)
	}
	return a.reflectAccessor.Addr(ptr, i)
}

func (a *composedAccessor) GetAssoc(ptr any, i int) any {
	if g := a.assocs[i].Get; g != nil {
		return g(ptr)
	}
	return a.reflectAccessor.GetAssoc(ptr, i)
}

func (a *composedAccessor) AssocAddr(ptr any, i int) any {
	if f := a.assocs[i].Addr; f != nil {
		return f(ptr)
	}
	return a.reflectAccessor.AssocAddr(ptr, i)
}

// accessible reports whether generated code in the entity's package can
// reach the field at index (every path segment exported or declared in the
// same package).
func accessible(t reflect.Type, index []int) bool {
	pkg := t.PkgPath()
	for _, x := range index {
		f := t.Field(x)
		if !f.IsExported() && f.PkgPath != pkg {
			return false
		}
		t = f.Type
	}
	return true
}

// StaleGenerated lists differences between the registered generated code and
// the current mapping, e.g. fields added after the last generation. It
// returns nil when no generated code is registered or it is up to date.
func (e *Entity) StaleGenerated() []string {
	generatedMu.RLock()
	g, ok := generatedTypes[e.GoType]
	generatedMu.RUnlock()
	if !ok {
		return nil
	}
	var out []string
	check := func(kind string, want map[string]bool, have map[string]GeneratedField) {
		for p := range want {
			if _, ok := have[p]; !ok {
				out = append(out, e.Name+": "+kind+" "+p+" is not generated")
			}
		}
		for p := range have {
			if !want[p] {
				out = append(out, e.Name+": generated "+kind+" "+p+" is no longer mapped")
			}
		}
	}
	fields, assocs := map[string]bool{}, map[string]bool{}
	for _, f := range e.Fields {
		if f.Index != nil && accessible(e.GoType, f.Index) {
			fields[path(e.GoType, f.Index)] = true
		}
	}
	for _, a := range e.Assocs {
		if accessible(e.GoType, a.Index) {
			assocs[path(e.GoType, a.Index)] = true
		}
	}
	check("field", fields, g.Fields)
	check("association", assocs, g.Assocs)
	slices.Sort(out)
	return out
}
