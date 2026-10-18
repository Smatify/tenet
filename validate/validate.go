// Package validate implements schema:validate: it checks the entity mapping
// for consistency, compares it with the database and detects stale
// generated code.
package validate

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/types"
)

// Report is the result of a validation run.
type Report struct {
	Mapping    []string     // mapping problems
	Generated  []string     // stale generated code
	Diff       *schema.Diff // differences between mapping and database (nil if not checked)
	Statements []string     // SQL that would bring the database in sync
}

// MappingOK reports whether the mapping and generated code are valid.
func (r *Report) MappingOK() bool { return len(r.Mapping) == 0 && len(r.Generated) == 0 }

// DatabaseOK reports whether the database matches the mapping (true if not checked).
func (r *Report) DatabaseOK() bool { return r.Diff == nil || r.Diff.IsEmpty() }

// OK reports whether everything is valid.
func (r *Report) OK() bool { return r.MappingOK() && r.DatabaseOK() }

// String renders the report like Doctrine's schema:validate.
func (r *Report) String() string {
	var b strings.Builder
	b.WriteString("Mapping\n-------\n")
	if r.MappingOK() {
		b.WriteString("[OK] The mapping is correct.\n")
	} else {
		for _, m := range slices.Concat(r.Mapping, r.Generated) {
			b.WriteString("[ERROR] " + m + "\n")
		}
	}
	b.WriteString("\nDatabase\n--------\n")
	switch {
	case r.Diff == nil:
		b.WriteString("[SKIPPED] The database was not checked.\n")
	case r.Diff.IsEmpty():
		b.WriteString("[OK] The database schema is in sync with the mapping.\n")
	default:
		b.WriteString("[ERROR] The database schema is not in sync with the mapping:\n")
		b.WriteString(r.Diff.String())
	}
	return b.String()
}

// Options configures a validation run.
type Options struct {
	Entities []*mapping.Entity
	// DB enables the database check when set.
	DB dbal.IDB
	// Ignore excludes tables from the database comparison (e.g. the
	// migrations table). Tables not mapped by any entity are reported as
	// "drop table" unless ignored.
	Ignore func(table string) bool
}

// Run validates and returns the report. An error is returned only when the
// database cannot be inspected.
func Run(ctx context.Context, opts Options) (*Report, error) {
	r := &Report{Mapping: CheckMapping(opts.Entities, dialectOf(opts.DB))}
	for _, e := range opts.Entities {
		r.Generated = append(r.Generated, e.StaleGenerated()...)
	}
	if opts.DB == nil {
		return r, nil
	}
	d := opts.DB.Dialect()
	current, err := schema.Introspect(ctx, opts.DB, d)
	if err != nil {
		return nil, err
	}
	want := mapping.BuildSchema(opts.Entities)
	ddl := schema.NewDDL(d)
	r.Diff = schema.NewComparator(d).IgnoreTables(opts.Ignore).Compare(current, want)
	if r.Statements, err = ddl.Migrate(r.Diff); err != nil {
		r.Mapping = append(r.Mapping, err.Error())
	}
	return r, nil
}

func dialectOf(db dbal.IDB) dialect.Dialect {
	if db == nil {
		return nil
	}
	return db.Dialect()
}

var versionTypes = []reflect.Type{
	reflect.TypeFor[int](), reflect.TypeFor[int32](), reflect.TypeFor[int64](), reflect.TypeFor[time.Time](),
}

// CheckMapping finds problems that parsing does not reject because they
// depend on the combination of entities or on the target database (d may be nil).
func CheckMapping(entities []*mapping.Entity, d dialect.Dialect) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	tables := map[string]string{}
	indexes := map[string]string{}
	for _, e := range entities {
		key := strings.ToLower(e.Table)
		if other, ok := tables[key]; ok {
			add("%s and %s are both mapped to table %q", other, e.Name, e.Table)
		}
		tables[key] = e.Name
		for _, i := range e.Indexes {
			k := strings.ToLower(i.Name)
			if other, ok := indexes[k]; ok && other != e.Table {
				add("index name %q is used by tables %s and %s; index names must be unique per schema", i.Name, other, e.Table)
			}
			indexes[k] = e.Table
		}
		if e.Version != nil && !slices.Contains(versionTypes, e.Version.GoType) {
			add("%s.%s: version fields must be int, int32, int64 or time.Time, got %s", e.Name, e.Version.Name, e.Version.GoType)
		}
		if e.Generator == mapping.GenIdentity {
			if id := e.SingleID(); id == nil || !isInteger(id.Type.Name()) {
				add("%s: identity generation requires a single integer identifier", e.Name)
			}
		}
		if (e.Generator == mapping.GenUUIDv7 || e.Generator == mapping.GenUUIDv4) && (e.SingleID() == nil || e.SingleID().Type.Name() != types.UUID) {
			add("%s: UUID generation requires a single uuid identifier", e.Name)
		}
		if d != nil && e.Generator == mapping.GenSequence && !d.Features().Has(dialect.FeatureSequences) {
			add("%s: %s does not support sequences; use identity generation", e.Name, d.Name())
		}
		for _, a := range e.Assocs {
			if a.OrderBy != "" {
				for _, part := range strings.Split(a.OrderBy, ",") {
					name := strings.Fields(strings.TrimSpace(part))
					if len(name) == 0 || (a.Target.Field(name[0]) == nil && a.Target.FieldByColumn(name[0]) == nil) {
						add("%s.%s: orderBy references unknown field %q of %s", e.Name, a.Name, part, a.Target.Name)
					}
				}
			}
			if a.Kind == mapping.OneToMany && a.OrphanRemoval {
				if inv := a.Inverse(); inv != nil && inv.Cascade.Has(mapping.CascadeRemove) {
					add("%s.%s: orphanRemoval with cascade remove on the owning side %s.%s deletes the parent", e.Name, a.Name, a.Target.Name, inv.Name)
				}
			}
			if !slices.Contains(entities, a.Target) {
				add("%s.%s: target entity %s is not part of the validated set", e.Name, a.Name, a.Target.Name)
			}
		}
	}
	return out
}

func isInteger(n types.Name) bool {
	return n == types.SmallInt || n == types.Integer || n == types.BigInt
}
