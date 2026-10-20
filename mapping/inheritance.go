package mapping

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/smatify/smorm/internal/brand"
)

func (e *Entity) byColumnHas(col string) bool {
	for _, f := range e.Fields {
		if strings.EqualFold(f.Column, col) {
			return true
		}
	}
	return false
}

var subclassOptions = map[string]bool{"discriminatorValue": true, "table": true, "abstract": true}

// parseSubclass maps a struct embedding a parent entity. The parent's
// fields and associations are inherited with index paths through the
// embedded field.
func (r *Registry) parseSubclass(e *Entity, pf reflect.StructField) (*Entity, error) {
	errf := func(format string, args ...any) error {
		return &MappingError{Entity: e.Name, Msg: fmt.Sprintf(format, args...)}
	}
	parent, err := r.get(pf.Type)
	if err != nil {
		return nil, err
	}
	if parent.Inheritance == "" {
		return nil, errf("embeds entity %s, which does not declare inheritance (add inheritance:single or inheritance:joined to its header tag)", parent.Name)
	}
	tag, err := ParseTag(pf.Tag.Get(brand.TagKey))
	if err != nil {
		return nil, errf("%v", err)
	}
	for _, k := range tag.Keys() {
		if !subclassOptions[k] {
			return nil, errf("unknown subclass tag option %q", k)
		}
	}
	e.Parent, e.Root, e.Inheritance = parent, parent.Root, parent.Inheritance
	e.ParentIndex = pf.Index
	e.HeaderIndex = append(slices.Clone(pf.Index), parent.HeaderIndex...)
	e.DiscriminatorColumn = parent.DiscriminatorColumn
	e.DiscriminatorValue = tag.Get("discriminatorValue")
	if e.DiscriminatorValue == "" {
		e.DiscriminatorValue = r.naming.TableName(e.Name)
	}
	e.Abstract = tag.Has("abstract")
	if other := e.Root.ClassForDiscriminator(e.DiscriminatorValue); other != nil {
		return nil, errf("discriminator value %q is already used by %s", e.DiscriminatorValue, other.Name)
	}
	switch {
	case e.Inheritance == SingleTable && tag.Has("table"):
		return nil, errf("single table inheritance stores %s in %s; remove table", e.Name, e.Root.Table)
	case e.Inheritance == SingleTable:
		e.Table = e.Root.Table
	case tag.Get("table") != "":
		e.Table = tag.Get("table")
	default:
		e.Table = r.naming.TableName(e.Name)
	}
	e.Repository = ""
	e.ID, e.Generator, e.Sequence = nil, parent.Generator, parent.Sequence

	// Inherit fields and associations.
	assocs := map[*Association]*Association{}
	for _, a := range parent.Assocs {
		c := *a
		c.Index = append(slices.Clone(pf.Index), a.Index...)
		c.Source = e
		c.Inherited = true
		assocs[a] = &c
		e.Assocs = append(e.Assocs, &c)
	}
	for _, f := range parent.Fields {
		c := *f
		if f.Index != nil {
			c.Index = append(slices.Clone(pf.Index), f.Index...)
		}
		if f.Assoc != nil {
			c.Assoc = assocs[f.Assoc]
		}
		e.Fields = append(e.Fields, &c)
		if c.ID {
			e.ID = append(e.ID, &c)
		}
		if c.Version {
			e.Version = &c
		}
	}
	e.Indexes = nil
	own := len(e.Fields)
	t := e.GoType
	for i := range t.NumField() {
		if i == pf.Index[0] {
			continue
		}
		if err := r.walkField(e, t, i, nil, embedCtx{}); err != nil {
			return nil, err
		}
	}
	for _, f := range e.Fields[own:] {
		if f.ID {
			return nil, &MappingError{Entity: e.Name, Field: f.Name, Msg: "subclasses inherit the identifier"}
		}
		f.Table = e.Table
		if e.Inheritance == SingleTable {
			f.Nullable = true // other classes leave the column empty
		}
		if !f.Exported && f.Name != "" {
			e.DoctrineStyle = true
		}
	}
	e.DoctrineStyle = e.DoctrineStyle || parent.DoctrineStyle
	parent.Subclasses = append(parent.Subclasses, e)
	e.index()
	return e, nil
}
