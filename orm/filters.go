package orm

import (
	"reflect"
	"slices"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
)

// Filter adds a condition to every query that loads entities, like
// Doctrine's SQL filters. column returns the qualified column reference for
// a column of the filtered entity.
type Filter interface {
	Condition(meta *mapping.Entity, column func(name string) dbal.Ident) (dbal.Expr, bool)
}

// FilterFunc adapts a function to Filter.
type FilterFunc func(meta *mapping.Entity, column func(name string) dbal.Ident) (dbal.Expr, bool)

// Condition implements Filter.
func (f FilterFunc) Condition(meta *mapping.Entity, column func(string) dbal.Ident) (dbal.Expr, bool) {
	return f(meta, column)
}

// ColumnFilter restricts every entity that has column to rows where it
// equals value, e.g. ColumnFilter("tenant_id", tenantID).
func ColumnFilter(column string, value any) Filter {
	return FilterFunc(func(meta *mapping.Entity, col func(string) dbal.Ident) (dbal.Expr, bool) {
		f := meta.FieldByColumn(column)
		if f == nil {
			return nil, false
		}
		return dbal.SQL("? = ?", col(f.Column), idArg{f: f, v: value}), true
	})
}

// SoftDeletable is implemented by entities that are marked as deleted
// instead of being removed (usually through the SoftDelete trait).
type SoftDeletable interface {
	MarkDeleted(now time.Time)
	IsDeleted() bool
	DeletedColumn() string
}

var softDeletableType = reflect.TypeFor[SoftDeletable]()

// SoftDeleteFilterName is the name of the built-in filter that hides
// soft-deleted entities. It is enabled by default.
const SoftDeleteFilterName = "softdelete"

// SoftDeleteFilter hides soft-deleted rows.
var SoftDeleteFilter Filter = FilterFunc(func(meta *mapping.Entity, col func(string) dbal.Ident) (dbal.Expr, bool) {
	if !reflect.PointerTo(meta.GoType).Implements(softDeletableType) {
		return nil, false
	}
	sd := meta.Accessor().New().(SoftDeletable)
	return dbal.SQL("? IS NULL", col(sd.DeletedColumn())), true
})

// Filters is the set of enabled filters of an entity manager.
type Filters struct {
	names   []string
	filters map[string]Filter
}

func newFilters() *Filters {
	f := &Filters{filters: map[string]Filter{}}
	f.Enable(SoftDeleteFilterName, SoftDeleteFilter)
	return f
}

// Enable adds or replaces a filter.
func (f *Filters) Enable(name string, filter Filter) {
	if _, ok := f.filters[name]; !ok {
		f.names = append(f.names, name)
	}
	f.filters[name] = filter
}

// Disable removes a filter.
func (f *Filters) Disable(name string) {
	delete(f.filters, name)
	f.names = slices.DeleteFunc(f.names, func(n string) bool { return n == name })
}

// IsEnabled reports whether a filter is enabled.
func (f *Filters) IsEnabled(name string) bool { _, ok := f.filters[name]; return ok }

// Enabled returns the names of the enabled filters.
func (f *Filters) Enabled() []string { return slices.Clone(f.names) }

// Filters returns the filters of the entity manager.
func (em *EntityManager) Filters() *Filters { return em.filters }

// filterConds returns the conditions of all enabled filters for meta, with
// columns qualified by alias ("" for none).
func (em *EntityManager) filterConds(meta *mapping.Entity, alias string) []dbal.Expr {
	var out []dbal.Expr
	col := func(name string) dbal.Ident {
		if alias == "" {
			return dbal.Ident(name)
		}
		return dbal.Ident(alias + "." + name)
	}
	for _, n := range em.filters.names {
		if e, ok := em.filters.filters[n].Condition(meta, col); ok {
			out = append(out, e)
		}
	}
	return out
}

func colIdent(name string) dbal.Ident { return dbal.Ident(name) }
