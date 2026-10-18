// Package schema is a database independent model of tables, columns,
// indexes, foreign keys and sequences. It is produced from entity mappings
// and from database introspection, and compared to generate migrations.
package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/smatify/smorm/types"
)

// Schema is a set of tables and sequences.
type Schema struct {
	Tables    []*Table
	Sequences []*Sequence
}

// New returns an empty schema.
func New() *Schema { return &Schema{} }

// Table returns the table with name (case-insensitive) or nil.
func (s *Schema) Table(name string) *Table {
	for _, t := range s.Tables {
		if strings.EqualFold(t.Name, name) {
			return t
		}
	}
	return nil
}

// HasTable reports whether a table exists.
func (s *Schema) HasTable(name string) bool { return s.Table(name) != nil }

// CreateTable adds and returns a new table. It panics if the table exists,
// because that is always a programming error in mapping code.
func (s *Schema) CreateTable(name string) *Table {
	if s.HasTable(name) {
		panic(fmt.Sprintf("schema: table %q already exists", name))
	}
	t := &Table{Name: name}
	s.Tables = append(s.Tables, t)
	return t
}

// DropTable removes a table.
func (s *Schema) DropTable(name string) {
	s.Tables = slices.DeleteFunc(s.Tables, func(t *Table) bool { return strings.EqualFold(t.Name, name) })
}

// Sequence returns the sequence with name or nil.
func (s *Schema) Sequence(name string) *Sequence {
	for _, q := range s.Sequences {
		if strings.EqualFold(q.Name, name) {
			return q
		}
	}
	return nil
}

// CreateSequence adds a sequence.
func (s *Schema) CreateSequence(name string, start, increment int64) *Sequence {
	q := &Sequence{Name: name, Start: start, Increment: increment}
	s.Sequences = append(s.Sequences, q)
	return q
}

// Validate checks every table.
func (s *Schema) Validate() error {
	var errs []error
	for _, t := range s.Tables {
		if err := t.Validate(); err != nil {
			errs = append(errs, err)
		}
		for _, fk := range t.ForeignKeys {
			ref := s.Table(fk.RefTable)
			if ref == nil {
				errs = append(errs, fmt.Errorf("%s: foreign key %s references unknown table %q", t.Name, fk.Name, fk.RefTable))
				continue
			}
			for _, c := range fk.RefColumns {
				if ref.Column(c) == nil {
					errs = append(errs, fmt.Errorf("%s: foreign key %s references unknown column %s.%s", t.Name, fk.Name, ref.Name, c))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// Clone returns a deep copy.
func (s *Schema) Clone() *Schema {
	c := &Schema{}
	for _, t := range s.Tables {
		c.Tables = append(c.Tables, t.Clone())
	}
	for _, q := range s.Sequences {
		cp := *q
		c.Sequences = append(c.Sequences, &cp)
	}
	return c
}

// Table is a database table.
type Table struct {
	Name        string
	Columns     []*Column
	PrimaryKey  *Index
	Indexes     []*Index
	ForeignKeys []*ForeignKey
	Comment     string
}

// Column returns the column with name (case-insensitive) or nil.
func (t *Table) Column(name string) *Column {
	for _, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return c
		}
	}
	return nil
}

// AddColumn appends a column and returns it for further configuration.
func (t *Table) AddColumn(name string, typ types.Name) *Column {
	c := &Column{Name: name, Type: typ}
	t.Columns = append(t.Columns, c)
	return c
}

// DropColumn removes a column.
func (t *Table) DropColumn(name string) {
	t.Columns = slices.DeleteFunc(t.Columns, func(c *Column) bool { return strings.EqualFold(c.Name, name) })
}

// SetPrimaryKey sets the primary key columns.
func (t *Table) SetPrimaryKey(cols ...string) *Index {
	t.PrimaryKey = &Index{Name: IdentifierName("pk", t.Name, nil), Columns: cols, Unique: true, Primary: true}
	return t.PrimaryKey
}

// AddIndex adds a non-unique index with a generated name.
func (t *Table) AddIndex(cols ...string) *Index {
	i := &Index{Name: IdentifierName("idx", t.Name, cols), Columns: cols}
	t.Indexes = append(t.Indexes, i)
	return i
}

// AddUniqueIndex adds a unique index with a generated name.
func (t *Table) AddUniqueIndex(cols ...string) *Index {
	i := &Index{Name: IdentifierName("uniq", t.Name, cols), Columns: cols, Unique: true}
	t.Indexes = append(t.Indexes, i)
	return i
}

// Index returns the index with name or nil.
func (t *Table) Index(name string) *Index {
	for _, i := range t.Indexes {
		if strings.EqualFold(i.Name, name) {
			return i
		}
	}
	return nil
}

// AddForeignKey adds a foreign key with a generated name.
func (t *Table) AddForeignKey(cols []string, refTable string, refCols []string) *ForeignKey {
	fk := &ForeignKey{Name: IdentifierName("fk", t.Name, cols), Columns: cols, RefTable: refTable, RefColumns: refCols}
	t.ForeignKeys = append(t.ForeignKeys, fk)
	return fk
}

// ForeignKey returns the foreign key with name or nil.
func (t *Table) ForeignKey(name string) *ForeignKey {
	for _, f := range t.ForeignKeys {
		if strings.EqualFold(f.Name, name) {
			return f
		}
	}
	return nil
}

// Validate checks internal consistency.
func (t *Table) Validate() error {
	var errs []error
	errf := func(format string, args ...any) { errs = append(errs, fmt.Errorf(t.Name+": "+format, args...)) }
	if len(t.Columns) == 0 {
		errf("table has no columns")
	}
	seen := map[string]bool{}
	autoInc := 0
	for _, c := range t.Columns {
		key := strings.ToLower(c.Name)
		if seen[key] {
			errf("duplicate column %q", c.Name)
		}
		seen[key] = true
		if c.AutoIncrement {
			autoInc++
			if !c.IsInteger() {
				errf("column %q: auto increment requires an integer type, got %s", c.Name, c.Type)
			}
		}
	}
	if autoInc > 1 {
		errf("more than one auto increment column")
	}
	checkCols := func(what string, cols []string) {
		if len(cols) == 0 {
			errf("%s has no columns", what)
		}
		for _, c := range cols {
			if !seen[strings.ToLower(c)] {
				errf("%s references unknown column %q", what, c)
			}
		}
	}
	if t.PrimaryKey != nil {
		checkCols("primary key", t.PrimaryKey.Columns)
		for _, c := range t.PrimaryKey.Columns {
			if col := t.Column(c); col != nil && col.Nullable {
				errf("primary key column %q must not be nullable", c)
			}
		}
	}
	names := map[string]bool{}
	for _, i := range t.Indexes {
		checkCols("index "+i.Name, i.Columns)
		if names[strings.ToLower(i.Name)] {
			errf("duplicate index name %q", i.Name)
		}
		names[strings.ToLower(i.Name)] = true
	}
	for _, f := range t.ForeignKeys {
		checkCols("foreign key "+f.Name, f.Columns)
		if len(f.Columns) != len(f.RefColumns) {
			errf("foreign key %s has %d columns but references %d", f.Name, len(f.Columns), len(f.RefColumns))
		}
	}
	return errors.Join(errs...)
}

// Clone returns a deep copy.
func (t *Table) Clone() *Table {
	c := &Table{Name: t.Name, Comment: t.Comment}
	for _, col := range t.Columns {
		c.Columns = append(c.Columns, col.Clone())
	}
	if t.PrimaryKey != nil {
		c.PrimaryKey = t.PrimaryKey.Clone()
	}
	for _, i := range t.Indexes {
		c.Indexes = append(c.Indexes, i.Clone())
	}
	for _, f := range t.ForeignKeys {
		c.ForeignKeys = append(c.ForeignKeys, f.Clone())
	}
	return c
}

// Column is a table column.
type Column struct {
	Name          string
	Type          types.Name
	Options       types.Options
	Nullable      bool
	Default       *string // raw SQL expression, nil means no default
	AutoIncrement bool
	Comment       string
	// RawType is a verbatim SQL type used for database types smorm does not
	// map (filled by introspection). It takes precedence over Type.
	RawType string
	// DefaultName is the name of the default constraint on SQL Server when it
	// differs from DefaultConstraintName (filled by introspection).
	DefaultName string
}

// SetNullable sets nullability and returns c for chaining.
func (c *Column) SetNullable(v bool) *Column { c.Nullable = v; return c }

// SetLength sets the length option.
func (c *Column) SetLength(n int) *Column { c.Options.Length = n; return c }

// SetPrecision sets precision and scale.
func (c *Column) SetPrecision(p, s int) *Column {
	c.Options.Precision, c.Options.Scale = p, s
	return c
}

// SetFixed makes string/binary columns fixed length.
func (c *Column) SetFixed(v bool) *Column { c.Options.Fixed = v; return c }

// SetDefault sets a raw SQL default expression, e.g. "0", "'draft'" or
// "CURRENT_TIMESTAMP".
func (c *Column) SetDefault(expr string) *Column { c.Default = &expr; return c }

// SetAutoIncrement marks the column as auto increment / identity.
func (c *Column) SetAutoIncrement(v bool) *Column { c.AutoIncrement = v; return c }

// SetComment sets the column comment.
func (c *Column) SetComment(s string) *Column { c.Comment = s; return c }

// IsInteger reports whether the column has an integer type.
func (c *Column) IsInteger() bool {
	return c.Type == types.SmallInt || c.Type == types.Integer || c.Type == types.BigInt
}

// Clone returns a copy.
func (c *Column) Clone() *Column {
	cp := *c
	if c.Default != nil {
		d := *c.Default
		cp.Default = &d
	}
	return &cp
}

// Index is a (unique) index or the primary key.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
	Primary bool
	Where   string // partial index predicate (Postgres, SQLite, SQL Server)
}

// SetName overrides the generated name.
func (i *Index) SetName(n string) *Index { i.Name = n; return i }

// SetWhere makes the index partial.
func (i *Index) SetWhere(pred string) *Index { i.Where = pred; return i }

// Clone returns a copy.
func (i *Index) Clone() *Index {
	cp := *i
	cp.Columns = slices.Clone(i.Columns)
	return &cp
}

// SameColumns reports whether both indexes cover the same columns in order.
func (i *Index) SameColumns(o *Index) bool {
	return slices.EqualFunc(i.Columns, o.Columns, strings.EqualFold)
}

// Action is a referential action for ON DELETE / ON UPDATE.
type Action string

// Referential actions.
const (
	NoAction   Action = ""
	Restrict   Action = "RESTRICT"
	Cascade    Action = "CASCADE"
	SetNull    Action = "SET NULL"
	SetDefault Action = "SET DEFAULT"
)

// ForeignKey is a foreign key constraint.
type ForeignKey struct {
	Name       string
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   Action
	OnUpdate   Action
}

// SetName overrides the generated name.
func (f *ForeignKey) SetName(n string) *ForeignKey { f.Name = n; return f }

// SetOnDelete sets the ON DELETE action.
func (f *ForeignKey) SetOnDelete(a Action) *ForeignKey { f.OnDelete = a; return f }

// SetOnUpdate sets the ON UPDATE action.
func (f *ForeignKey) SetOnUpdate(a Action) *ForeignKey { f.OnUpdate = a; return f }

// Clone returns a copy.
func (f *ForeignKey) Clone() *ForeignKey {
	cp := *f
	cp.Columns = slices.Clone(f.Columns)
	cp.RefColumns = slices.Clone(f.RefColumns)
	return &cp
}

// Sequence is a database sequence.
type Sequence struct {
	Name      string
	Start     int64
	Increment int64
}

// MaxIdentifierLength is the shortest identifier limit of all supported
// databases (Postgres: 63, MySQL: 64, SQL Server: 128).
const MaxIdentifierLength = 63

// IdentifierName builds a deterministic, readable constraint name such as
// "idx_users_email". Names longer than MaxIdentifierLength are shortened and
// suffixed with a hash so they stay unique.
func IdentifierName(prefix, table string, cols []string) string {
	parts := append([]string{prefix, table}, cols...)
	name := strings.ToLower(strings.Join(parts, "_"))
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if len(name) <= MaxIdentifierLength {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(sum[:])[:8]
	return name[:MaxIdentifierLength-9] + "_" + hash
}
