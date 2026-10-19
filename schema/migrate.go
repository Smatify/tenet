package schema

import (
	"fmt"
	"slices"
	"strings"

	"github.com/smatify/smorm/dialect"
)

// SQLiteRebuildPrefix prefixes the temporary table used when SQLite has to
// rebuild a table to apply changes ALTER TABLE cannot express.
const SQLiteRebuildPrefix = "__tmp__"

// Migrate returns the statements that apply diff. The order avoids
// dependency problems: foreign keys are dropped first and added last.
//
// SQLite rebuilds changed tables (create copy, move rows, swap). Run those
// statements with foreign key enforcement disabled; they include the
// necessary PRAGMA statements, which only take effect outside a transaction.
func (g *DDL) Migrate(diff *Diff) ([]string, error) {
	var (
		dropFKs, pre, creates, alters, drops, addFKs, post []string
	)
	dropped := map[string]bool{}
	for _, t := range dropOrder(diff.DropTables) {
		dropped[strings.ToLower(t.Name)] = true
		for _, fk := range t.ForeignKeys {
			if s, ok := g.DropForeignKey(t.Name, fk); ok {
				dropFKs = append(dropFKs, s)
			}
		}
		drops = append(drops, g.DropTable(t.Name))
	}
	for _, s := range diff.CreateSequences {
		pre = append(pre, g.CreateSequence(s))
	}
	for _, s := range diff.DropSequences {
		post = append(post, g.DropSequence(s.Name))
	}
	// Sequences that are recreated must be dropped before they are created.
	for _, s := range diff.DropSequences {
		if slices.ContainsFunc(diff.CreateSequences, func(c *Sequence) bool { return strings.EqualFold(c.Name, s.Name) }) {
			pre = append([]string{g.DropSequence(s.Name)}, pre...)
			post = slices.DeleteFunc(post, func(q string) bool { return q == g.DropSequence(s.Name) })
		}
	}
	for _, t := range diff.CreateTables {
		stmts, err := g.CreateTable(t)
		if err != nil {
			return nil, err
		}
		creates = append(creates, stmts...)
		for _, fk := range t.ForeignKeys {
			if s, ok := g.AddForeignKey(t.Name, fk); ok {
				addFKs = append(addFKs, s)
			}
		}
	}
	rebuilt := false
	for _, td := range diff.AlterTables {
		if g.name() == dialect.SQLite && sqliteNeedsRebuild(td) {
			stmts, err := g.sqliteRebuild(td)
			if err != nil {
				return nil, err
			}
			alters = append(alters, stmts...)
			rebuilt = true
			continue
		}
		for _, fk := range td.DropForeignKeys {
			if s, ok := g.DropForeignKey(td.From.Name, fk); ok {
				dropFKs = append(dropFKs, s)
			}
		}
		stmts, err := g.alterTable(td)
		if err != nil {
			return nil, err
		}
		alters = append(alters, stmts...)
		for _, fk := range td.AddForeignKeys {
			if s, ok := g.AddForeignKey(td.To.Name, fk); ok {
				addFKs = append(addFKs, s)
			}
		}
	}
	out := slices.Concat(dropFKs, pre, creates, alters, drops, addFKs, post)
	if rebuilt {
		out = slices.Concat([]string{"PRAGMA foreign_keys = OFF"}, out, []string{"PRAGMA foreign_keys = ON"})
	}
	return out, nil
}

// MigrateSchemas compares from and to and returns the migration statements.
func (g *DDL) MigrateSchemas(from, to *Schema, ignore func(string) bool) ([]string, error) {
	c := &Comparator{ddl: g, ignore: ignore}
	return g.Migrate(c.Compare(from, to))
}

func (g *DDL) alterPrefix(table string) string { return "ALTER TABLE " + g.q(table) + " " }

func (g *DDL) alterTable(td *TableDiff) ([]string, error) {
	t := td.To
	alter := g.alterPrefix(t.Name)
	var out, after []string

	// Drop indexes first: they may cover columns that change or disappear.
	dropIdx := slices.Clone(td.DropIndexes)
	addIdx := slices.Clone(td.AddIndexes)
	if g.name() == dialect.MSSQL {
		// SQL Server refuses to alter columns that are part of an index.
		for _, ch := range td.ChangeColumns {
			if !ch.Type && !ch.Nullable {
				continue
			}
			for _, i := range td.From.Indexes {
				if slices.ContainsFunc(i.Columns, func(c string) bool { return strings.EqualFold(c, ch.From.Name) }) &&
					!slices.Contains(dropIdx, i) {
					dropIdx = append(dropIdx, i)
					if ni := t.Index(i.Name); ni != nil && !slices.Contains(addIdx, ni) {
						addIdx = append(addIdx, ni)
					}
				}
			}
		}
	}
	for _, i := range dropIdx {
		out = append(out, g.DropIndex(t.Name, i))
	}
	if td.PrimaryKeyChanged && td.From.PrimaryKey != nil {
		if g.isMySQL() {
			out = append(out, alter+"DROP PRIMARY KEY")
		} else {
			out = append(out, alter+"DROP CONSTRAINT "+g.q(td.From.PrimaryKey.Name))
		}
	}
	for _, c := range td.AddColumns {
		decl, err := g.ColumnDeclaration(t, c)
		if err != nil {
			return nil, err
		}
		if g.name() == dialect.MSSQL {
			out = append(out, alter+"ADD "+decl)
		} else {
			out = append(out, alter+"ADD COLUMN "+decl)
		}
		if g.name() == dialect.Postgres && c.Comment != "" {
			after = append(after, g.columnComment(t.Name, c))
		}
	}
	for _, ch := range td.ChangeColumns {
		stmts, err := g.changeColumn(td, ch)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
	}
	for _, c := range td.DropColumns {
		if g.name() == dialect.MSSQL && c.Default != nil {
			out = append(out, alter+"DROP CONSTRAINT "+g.q(g.defaultName(t.Name, c)))
		}
		out = append(out, alter+"DROP COLUMN "+g.q(c.Name))
	}
	if td.PrimaryKeyChanged && t.PrimaryKey != nil {
		if g.isMySQL() {
			out = append(out, alter+"ADD PRIMARY KEY ("+g.qList(t.PrimaryKey.Columns)+")")
		} else {
			out = append(out, alter+"ADD CONSTRAINT "+g.q(t.PrimaryKey.Name)+" PRIMARY KEY ("+g.qList(t.PrimaryKey.Columns)+")")
		}
	}
	for _, i := range addIdx {
		out = append(out, g.CreateIndex(t.Name, i))
	}
	if td.CommentChanged {
		out = append(out, g.tableComment(t))
	}
	return append(out, after...), nil
}

func (g *DDL) defaultName(table string, c *Column) string {
	if c.DefaultName != "" {
		return c.DefaultName
	}
	return DefaultConstraintName(table, c.Name)
}

func (g *DDL) changeColumn(td *TableDiff, ch *ColumnChange) ([]string, error) {
	t, c := td.To, ch.To
	alter := g.alterPrefix(t.Name)
	col := g.q(c.Name)
	switch {
	case g.isMySQL():
		decl, err := g.ColumnDeclaration(t, c)
		if err != nil {
			return nil, err
		}
		return []string{alter + "MODIFY " + decl}, nil

	case g.name() == dialect.Postgres:
		var out []string
		if ch.Type {
			typ, err := g.TypeDeclaration(c)
			if err != nil {
				return nil, err
			}
			out = append(out, alter+"ALTER COLUMN "+col+" TYPE "+typ+" USING "+col+"::"+typ)
		}
		if ch.AutoIncrement {
			if c.AutoIncrement {
				out = append(out, alter+"ALTER COLUMN "+col+" DROP DEFAULT", alter+"ALTER COLUMN "+col+" ADD GENERATED BY DEFAULT AS IDENTITY")
			} else {
				out = append(out, alter+"ALTER COLUMN "+col+" DROP IDENTITY IF EXISTS")
			}
		}
		if ch.Nullable {
			if c.Nullable {
				out = append(out, alter+"ALTER COLUMN "+col+" DROP NOT NULL")
			} else {
				out = append(out, alter+"ALTER COLUMN "+col+" SET NOT NULL")
			}
		}
		if ch.Default || (ch.AutoIncrement && !c.AutoIncrement && c.Default != nil) {
			if c.Default == nil {
				out = append(out, alter+"ALTER COLUMN "+col+" DROP DEFAULT")
			} else {
				out = append(out, alter+"ALTER COLUMN "+col+" SET DEFAULT "+*c.Default)
			}
		}
		if ch.Comment {
			out = append(out, g.columnComment(t.Name, c))
		}
		return out, nil

	case g.name() == dialect.MSSQL:
		if ch.AutoIncrement {
			return nil, fmt.Errorf("schema: SQL Server cannot add or remove IDENTITY on %s.%s; recreate the column", t.Name, c.Name)
		}
		var out []string
		from := ch.From
		if from.Default != nil && (ch.Default || ch.Type) {
			out = append(out, alter+"DROP CONSTRAINT "+g.q(g.defaultName(td.From.Name, from)))
		}
		if ch.Type || ch.Nullable {
			typ, err := g.TypeDeclaration(c)
			if err != nil {
				return nil, err
			}
			null := " NOT NULL"
			if c.Nullable {
				null = " NULL"
			}
			out = append(out, alter+"ALTER COLUMN "+col+" "+typ+null)
		}
		if c.Default != nil && (ch.Default || ch.Type) {
			out = append(out, alter+"ADD CONSTRAINT "+g.q(DefaultConstraintName(t.Name, c.Name))+" DEFAULT "+*c.Default+" FOR "+col)
		}
		return out, nil
	}
	return nil, fmt.Errorf("schema: altering columns is not supported for dialect %q", g.name())
}

// sqliteNeedsRebuild reports whether td needs more than SQLite's ALTER TABLE
// (which can only add simple columns) and CREATE/DROP INDEX.
func sqliteNeedsRebuild(td *TableDiff) bool {
	if len(td.DropColumns)+len(td.ChangeColumns)+len(td.AddForeignKeys)+len(td.DropForeignKeys) > 0 || td.PrimaryKeyChanged {
		return true
	}
	for _, c := range td.AddColumns {
		// ADD COLUMN cannot add NOT NULL columns without default or
		// non-constant defaults such as CURRENT_TIMESTAMP.
		if (!c.Nullable && c.Default == nil) || c.AutoIncrement ||
			(c.Default != nil && NormalizeDefault(dialect.SQLite, *c.Default) == "CURRENT_TIMESTAMP") {
			return true
		}
	}
	return false
}

// sqliteRebuild implements the "12 step" table rebuild documented by SQLite.
func (g *DDL) sqliteRebuild(td *TableDiff) ([]string, error) {
	t := td.To
	tmp := SQLiteRebuildPrefix + t.Name
	create, err := g.createTableStmt(t, tmp)
	if err != nil {
		return nil, err
	}
	var cols []string
	for _, c := range t.Columns {
		if td.From.Column(c.Name) != nil {
			cols = append(cols, c.Name)
		}
	}
	out := []string{create}
	if len(cols) > 0 {
		list := g.qList(cols)
		out = append(out, "INSERT INTO "+g.q(tmp)+" ("+list+") SELECT "+list+" FROM "+g.q(td.From.Name))
	}
	out = append(out,
		"DROP TABLE "+g.q(td.From.Name),
		"ALTER TABLE "+g.q(tmp)+" RENAME TO "+g.q(t.Name),
	)
	for _, i := range t.Indexes {
		out = append(out, g.CreateIndex(t.Name, i))
	}
	return out, nil
}

// DependencyOrder sorts tables so that tables referencing others come
// first (the order for dropping tables or deleting their rows).
func DependencyOrder(tables []*Table) []*Table { return dropOrder(tables) }

// dropOrder sorts tables so that referencing tables are dropped before the
// tables they reference (required where foreign keys cannot be dropped
// first, e.g. SQLite).
func dropOrder(tables []*Table) []*Table {
	byName := map[string]*Table{}
	for _, t := range tables {
		byName[strings.ToLower(t.Name)] = t
	}
	referencedBy := map[*Table][]*Table{}
	for _, t := range tables {
		for _, fk := range t.ForeignKeys {
			if ref := byName[strings.ToLower(fk.RefTable)]; ref != nil && ref != t {
				referencedBy[ref] = append(referencedBy[ref], t)
			}
		}
	}
	var out []*Table
	state := map[*Table]int{}
	var visit func(t *Table)
	visit = func(t *Table) {
		if state[t] != 0 {
			return // done or in a cycle; cycles are broken by dropping FKs first elsewhere
		}
		state[t] = 1
		for _, r := range referencedBy[t] {
			visit(r)
		}
		state[t] = 2
		out = append(out, t)
	}
	for _, t := range tables {
		visit(t)
	}
	return out
}
