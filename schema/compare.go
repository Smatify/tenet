package schema

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/smatify/tenet/dialect"
)

// Diff describes how to turn one schema into another.
type Diff struct {
	CreateTables    []*Table
	DropTables      []*Table
	AlterTables     []*TableDiff
	CreateSequences []*Sequence
	DropSequences   []*Sequence
}

// IsEmpty reports whether nothing changed.
func (d *Diff) IsEmpty() bool {
	return len(d.CreateTables)+len(d.DropTables)+len(d.AlterTables)+len(d.CreateSequences)+len(d.DropSequences) == 0
}

// TableDiff describes changes to a single table.
type TableDiff struct {
	From, To          *Table
	AddColumns        []*Column
	DropColumns       []*Column
	ChangeColumns     []*ColumnChange
	AddIndexes        []*Index
	DropIndexes       []*Index
	AddForeignKeys    []*ForeignKey
	DropForeignKeys   []*ForeignKey
	PrimaryKeyChanged bool
	CommentChanged    bool
}

// IsEmpty reports whether the table is unchanged.
func (t *TableDiff) IsEmpty() bool {
	return len(t.AddColumns)+len(t.DropColumns)+len(t.ChangeColumns)+len(t.AddIndexes)+len(t.DropIndexes)+
		len(t.AddForeignKeys)+len(t.DropForeignKeys) == 0 && !t.PrimaryKeyChanged && !t.CommentChanged
}

// ColumnChange lists what differs between two versions of a column.
type ColumnChange struct {
	From, To      *Column
	Type          bool
	Nullable      bool
	Default       bool
	AutoIncrement bool
	Comment       bool
}

// Comparator compares schemas the way a given database would see them.
type Comparator struct {
	ddl    *DDL
	ignore func(table string) bool
}

// NewComparator returns a comparator for d.
func NewComparator(d dialect.Dialect) *Comparator { return &Comparator{ddl: NewDDL(d)} }

// IgnoreTables excludes tables (e.g. the migrations table) from the diff.
func (c *Comparator) IgnoreTables(fn func(table string) bool) *Comparator {
	c.ignore = fn
	return c
}

func (c *Comparator) ignored(name string) bool { return c.ignore != nil && c.ignore(name) }

func (c *Comparator) name() dialect.Name { return c.ddl.name() }

func (c *Comparator) supportsComments() bool {
	n := c.name()
	return n == dialect.Postgres || n == dialect.MySQL || n == dialect.MariaDB
}

// Compare returns the changes needed to turn from into to.
func (c *Comparator) Compare(from, to *Schema) *Diff {
	d := &Diff{}
	for _, t := range to.Tables {
		if c.ignored(t.Name) {
			continue
		}
		old := from.Table(t.Name)
		if old == nil {
			d.CreateTables = append(d.CreateTables, t)
			continue
		}
		if td := c.CompareTables(old, t); !td.IsEmpty() {
			d.AlterTables = append(d.AlterTables, td)
		}
	}
	for _, t := range from.Tables {
		if !c.ignored(t.Name) && to.Table(t.Name) == nil {
			d.DropTables = append(d.DropTables, t)
		}
	}
	for _, s := range to.Sequences {
		old := from.Sequence(s.Name)
		if old == nil {
			d.CreateSequences = append(d.CreateSequences, s)
		} else if old.Start != s.Start || old.Increment != s.Increment {
			d.DropSequences = append(d.DropSequences, old)
			d.CreateSequences = append(d.CreateSequences, s)
		}
	}
	for _, s := range from.Sequences {
		if to.Sequence(s.Name) == nil {
			d.DropSequences = append(d.DropSequences, s)
		}
	}
	return d
}

// CompareTables returns the changes between two versions of a table.
func (c *Comparator) CompareTables(from, to *Table) *TableDiff {
	td := &TableDiff{From: from, To: to}
	for _, col := range to.Columns {
		old := from.Column(col.Name)
		if old == nil {
			td.AddColumns = append(td.AddColumns, col)
			continue
		}
		if ch := c.CompareColumns(from, to, old, col); ch != nil {
			td.ChangeColumns = append(td.ChangeColumns, ch)
		}
	}
	for _, col := range from.Columns {
		if to.Column(col.Name) == nil {
			td.DropColumns = append(td.DropColumns, col)
		}
	}
	td.PrimaryKeyChanged = !samePK(from.PrimaryKey, to.PrimaryKey)
	for _, i := range to.Indexes {
		old := from.Index(i.Name)
		if old == nil {
			td.AddIndexes = append(td.AddIndexes, i)
		} else if !c.sameIndex(old, i) {
			td.DropIndexes = append(td.DropIndexes, old)
			td.AddIndexes = append(td.AddIndexes, i)
		}
	}
	for _, i := range from.Indexes {
		if to.Index(i.Name) == nil {
			td.DropIndexes = append(td.DropIndexes, i)
		}
	}
	for _, fk := range to.ForeignKeys {
		old := from.ForeignKey(fk.Name)
		if old == nil {
			td.AddForeignKeys = append(td.AddForeignKeys, fk)
		} else if !c.sameForeignKey(old, fk) {
			td.DropForeignKeys = append(td.DropForeignKeys, old)
			td.AddForeignKeys = append(td.AddForeignKeys, fk)
		}
	}
	for _, fk := range from.ForeignKeys {
		if to.ForeignKey(fk.Name) == nil {
			td.DropForeignKeys = append(td.DropForeignKeys, fk)
		}
	}
	td.CommentChanged = c.supportsComments() && from.Comment != to.Comment
	return td
}

// CompareColumns returns nil when both columns are equivalent.
func (c *Comparator) CompareColumns(fromTable, toTable *Table, from, to *Column) *ColumnChange {
	ch := &ColumnChange{From: from, To: to}
	rowid := c.ddl.sqliteRowID(fromTable, from) && c.ddl.sqliteRowID(toTable, to)
	if !rowid {
		a, errA := c.ddl.TypeDeclaration(from)
		b, errB := c.ddl.TypeDeclaration(to)
		ch.Type = errA != nil || errB != nil || normalizeSpace(a) != normalizeSpace(b)
	}
	ch.Nullable = from.Nullable != to.Nullable && !rowid
	ch.AutoIncrement = from.AutoIncrement != to.AutoIncrement
	if !from.AutoIncrement && !to.AutoIncrement {
		ch.Default = !sameDefault(c.name(), from.Default, to.Default)
	}
	ch.Comment = c.supportsComments() && from.Comment != to.Comment
	if !ch.Type && !ch.Nullable && !ch.AutoIncrement && !ch.Default && !ch.Comment {
		return nil
	}
	return ch
}

func samePK(a, b *Index) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.SameColumns(b)
}

func (c *Comparator) sameIndex(a, b *Index) bool {
	if a.Unique != b.Unique || !a.SameColumns(b) {
		return false
	}
	for n := range a.Columns {
		if c.ddl.indexSort(a.SortOf(n)) != c.ddl.indexSort(b.SortOf(n)) {
			return false
		}
	}
	if c.name() == dialect.MySQL || c.name() == dialect.MariaDB {
		return true // no partial indexes
	}
	return normalizePredicate(a.Where) == normalizePredicate(b.Where)
}

func (c *Comparator) sameForeignKey(a, b *ForeignKey) bool {
	return strings.EqualFold(a.RefTable, b.RefTable) &&
		(&Index{Columns: a.Columns}).SameColumns(&Index{Columns: b.Columns}) &&
		(&Index{Columns: a.RefColumns}).SameColumns(&Index{Columns: b.RefColumns}) &&
		c.normAction(a.OnDelete) == c.normAction(b.OnDelete) && c.normAction(a.OnUpdate) == c.normAction(b.OnUpdate)
}

// normAction treats RESTRICT as NO ACTION where both behave identically.
func (c *Comparator) normAction(a Action) Action {
	if a == Restrict && c.name() != dialect.Postgres && c.name() != dialect.SQLite {
		return NoAction
	}
	return a
}

func normalizeSpace(s string) string { return strings.ToUpper(strings.Join(strings.Fields(s), " ")) }

// pgPredicateCast is a cast Postgres adds when it prints a predicate
// ((status)::text IS DISTINCT FROM …).
var pgPredicateCast = regexp.MustCompile(`::(character varying|bit varying|double precision|(timestamp|time)(\(\d+\))? with(out)? time zone|"?[a-z_][a-z0-9_]*"?)(\(\d+(,\s*\d+)?\))?(\[\])*`)

func normalizePredicate(s string) string {
	s = pgPredicateCast.ReplaceAllString(strings.ToLower(s), "")
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '(', ')', '"', '`', '[', ']':
			return -1
		}
		return r
	}, strings.ToLower(s))
}

var (
	pgCastRe = regexp.MustCompile(`^(.*)::[A-Za-z0-9_ "\[\]().,]+$`)
	nowRe    = regexp.MustCompile(`(?i)^(now\(\)|current_timestamp(\(\d*\))?|getdate\(\)|sysdatetime\(\)|datetime\('now'\))$`)
)

// NormalizeDefault turns a default expression into a canonical form so values
// written by tenet and values reported by the database compare equal.
func NormalizeDefault(d dialect.Name, s string) string {
	s = strings.TrimSpace(s)
	for {
		changed := false
		if len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' && balanced(s[1:len(s)-1]) {
			s, changed = strings.TrimSpace(s[1:len(s)-1]), true
		}
		if m := pgCastRe.FindStringSubmatch(s); m != nil && d == dialect.Postgres {
			s, changed = strings.TrimSpace(m[1]), true
		}
		if !changed {
			break
		}
	}
	if len(s) >= 3 && (s[0] == 'N' || s[0] == 'n') && s[1] == '\'' {
		s = s[1:]
	}
	if nowRe.MatchString(s) {
		return "CURRENT_TIMESTAMP"
	}
	switch strings.ToLower(s) {
	case "true":
		if d == dialect.Postgres {
			return "true"
		}
		return "1"
	case "false":
		if d == dialect.Postgres {
			return "false"
		}
		return "0"
	case "null":
		return ""
	}
	inner := s
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		inner = s[1 : len(s)-1]
	}
	if f, err := strconv.ParseFloat(inner, 64); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return s
}

func balanced(s string) bool {
	depth := 0
	inStr := false
	for _, r := range s {
		switch {
		case r == '\'':
			inStr = !inStr
		case inStr:
		case r == '(':
			depth++
		case r == ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func sameDefault(d dialect.Name, a, b *string) bool {
	na, nb := "", ""
	if a != nil {
		na = NormalizeDefault(d, *a)
	}
	if b != nil {
		nb = NormalizeDefault(d, *b)
	}
	return na == nb
}
