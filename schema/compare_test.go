package schema

import (
	"strings"
	"testing"

	"github.com/smatify/tenet/dialect"
	"github.com/smatify/tenet/internal/testdialect"
	"github.com/smatify/tenet/types"
)

func cmpFor(n dialect.Name) *Comparator { return NewComparator(testdialect.Like(n)) }

func TestCompareIdentical(t *testing.T) {
	for _, n := range []dialect.Name{dialect.Postgres, dialect.MySQL, dialect.MariaDB, dialect.SQLite, dialect.MSSQL} {
		if d := cmpFor(n).Compare(buildBlog(), buildBlog()); !d.IsEmpty() {
			t.Errorf("%s: identical schemas differ: %+v", n, d)
		}
	}
}

func TestCompareChanges(t *testing.T) {
	from := buildBlog()
	to := buildBlog()
	to.CreateTable("tags").AddColumn("id", types.Integer)
	from.CreateTable("old").AddColumn("id", types.Integer)
	from.CreateTable("ignored_x").AddColumn("id", types.Integer)
	to.CreateSequence("new_seq", 1, 1)
	to.Sequence("seq").Start = 5
	from.CreateSequence("gone", 1, 1)

	u := to.Table("users")
	u.Comment = "changed"
	u.AddColumn("nick", types.String)
	u.DropColumn("code")
	u.Column("email").SetLength(200).SetNullable(true).SetComment("c")
	u.Column("score").SetDefault("1")
	u.Column("id").SetAutoIncrement(false)
	u.Index("uniq_users_email").Unique = false
	u.AddIndex("bio")
	from.Table("users").AddIndex("score").SetName("old_idx")
	u.SetPrimaryKey("id", "email")

	p := to.Table("posts")
	p.ForeignKeys[0].OnDelete = SetNull
	p.AddForeignKey([]string{"id"}, "users", []string{"id"}).SetName("fk_new")
	from.Table("posts").AddForeignKey([]string{"id"}, "users", []string{"id"}).SetName("fk_old")

	d := cmpFor(dialect.Postgres).IgnoreTables(func(n string) bool { return n == "ignored_x" }).Compare(from, to)
	if len(d.CreateTables) != 1 || d.CreateTables[0].Name != "tags" || len(d.DropTables) != 1 || d.DropTables[0].Name != "old" {
		t.Fatalf("tables: %+v %+v", d.CreateTables, d.DropTables)
	}
	if len(d.CreateSequences) != 2 || len(d.DropSequences) != 2 {
		t.Fatalf("sequences: %+v %+v", d.CreateSequences, d.DropSequences)
	}
	var ut, pt *TableDiff
	for _, td := range d.AlterTables {
		switch td.To.Name {
		case "users":
			ut = td
		case "posts":
			pt = td
		}
	}
	if ut == nil || pt == nil {
		t.Fatal("missing table diffs")
	}
	if len(ut.AddColumns) != 1 || len(ut.DropColumns) != 1 || !ut.PrimaryKeyChanged || !ut.CommentChanged {
		t.Fatalf("users: %+v", ut)
	}
	changes := map[string]*ColumnChange{}
	for _, c := range ut.ChangeColumns {
		changes[c.To.Name] = c
	}
	if e := changes["email"]; e == nil || !e.Type || !e.Nullable || !e.Comment || e.Default {
		t.Fatalf("email change = %+v", e)
	}
	if s := changes["score"]; s == nil || !s.Default || s.Type {
		t.Fatalf("score change = %+v", s)
	}
	if i := changes["id"]; i == nil || !i.AutoIncrement {
		t.Fatalf("id change = %+v", i)
	}
	if len(ut.AddIndexes) != 2 || len(ut.DropIndexes) != 2 {
		t.Fatalf("indexes add=%v drop=%v", ut.AddIndexes, ut.DropIndexes)
	}
	if len(pt.AddForeignKeys) != 2 || len(pt.DropForeignKeys) != 2 {
		t.Fatalf("fks add=%v drop=%v", pt.AddForeignKeys, pt.DropForeignKeys)
	}
}

func TestCompareDialectEquivalences(t *testing.T) {
	from, to := buildBlog(), buildBlog()
	to.Table("posts").ForeignKeys[0].OnUpdate = NoAction
	if !cmpFor(dialect.MySQL).Compare(from, to).IsEmpty() {
		t.Fatal("RESTRICT == NO ACTION on MySQL")
	}
	if cmpFor(dialect.Postgres).Compare(from, to).IsEmpty() {
		t.Fatal("RESTRICT != NO ACTION on Postgres")
	}
	to = buildBlog()
	to.Table("posts").Indexes[0].Where = ""
	if !cmpFor(dialect.MariaDB).Compare(from, to).IsEmpty() {
		t.Fatal("partial predicates are ignored on MariaDB")
	}
	to = buildBlog()
	to.Table("users").Comment = "x"
	to.Table("users").Column("email").Comment = "x"
	if !cmpFor(dialect.SQLite).Compare(from, to).IsEmpty() {
		t.Fatal("comments are ignored on SQLite")
	}
	// SQLite rowid alias: INTEGER vs BIGINT and nullability do not matter.
	from, to = buildBlog(), buildBlog()
	from.Table("users").Column("id").Type = types.Integer
	if !cmpFor(dialect.SQLite).Compare(from, to).IsEmpty() {
		t.Fatal("sqlite rowid alias type")
	}
	if cmpFor(dialect.Postgres).Compare(from, to).IsEmpty() {
		t.Fatal("integer vs bigint on postgres")
	}
	// Equivalent declarations: uuid vs char(36) on MySQL.
	from, to = buildBlog(), buildBlog()
	from.Table("users").AddColumn("u", types.UUID)
	to.Table("users").AddColumn("u", types.String).SetLength(36).SetFixed(true)
	if !cmpFor(dialect.MySQL).Compare(from, to).IsEmpty() {
		t.Fatal("UUID equals CHAR(36) on MySQL")
	}
	// PK removed.
	from, to = buildBlog(), buildBlog()
	to.Table("users").PrimaryKey = nil
	if !cmpFor(dialect.Postgres).Compare(from, to).AlterTables[0].PrimaryKeyChanged {
		t.Fatal("pk removal")
	}
	from.Table("users").Column("email").Type = "nope"
	if d := cmpFor(dialect.Postgres).CompareColumns(from.Table("users"), to.Table("users"), from.Table("users").Column("email"), to.Table("users").Column("email")); d == nil || !d.Type {
		t.Fatal("unknown type counts as changed")
	}
}

func TestNormalizeDefault(t *testing.T) {
	tests := []struct {
		d       dialect.Name
		in, out string
	}{
		{dialect.Postgres, "'draft'::character varying", "'draft'"},
		{dialect.Postgres, "('x'::text)", "'x'"},
		{dialect.Postgres, "'0'::numeric", "0"},
		{dialect.Postgres, "now()", "CURRENT_TIMESTAMP"},
		{dialect.Postgres, "TRUE", "true"},
		{dialect.Postgres, "false", "false"},
		{dialect.MySQL, "CURRENT_TIMESTAMP(6)", "CURRENT_TIMESTAMP"},
		{dialect.MySQL, "0.00", "0"},
		{dialect.MySQL, "true", "1"},
		{dialect.MySQL, "FALSE", "0"},
		{dialect.MariaDB, "current_timestamp()", "CURRENT_TIMESTAMP"},
		{dialect.MSSQL, "((0))", "0"},
		{dialect.MSSQL, "(N'it''s')", "'it''s'"},
		{dialect.MSSQL, "(getdate())", "CURRENT_TIMESTAMP"},
		{dialect.SQLite, "NULL", ""},
		{dialect.SQLite, "(1) + (2)", "(1) + (2)"},
		{dialect.SQLite, "'(a'", "'(a'"},
		{dialect.SQLite, "(a))", "(a))"},
	}
	for _, tt := range tests {
		if got := NormalizeDefault(tt.d, tt.in); got != tt.out {
			t.Errorf("%s %q = %q, want %q", tt.d, tt.in, got, tt.out)
		}
	}
	s := "x"
	if sameDefault(dialect.Postgres, nil, &s) || !sameDefault(dialect.Postgres, nil, nil) {
		t.Fatal("sameDefault nil handling")
	}
}

func TestParseDeclAndActions(t *testing.T) {
	base, a, b, ok := parseDecl("decimal( 10 , 2 )")
	if !ok || base != "DECIMAL" || a != 10 || b != 2 {
		t.Fatalf("%s %d %d %v", base, a, b, ok)
	}
	if base, a, _, ok := parseDecl("NVARCHAR(MAX)"); !ok || base != "NVARCHAR" || a != 0 {
		t.Fatal("MAX")
	}
	if _, _, _, ok := parseDecl("weird((x"); ok {
		t.Fatal("garbage parsed")
	}
	for in, want := range map[string]Action{"SET_NULL": SetNull, "cascade": Cascade, "NO ACTION": NoAction, "restrict": Restrict, "set default": SetDefault} {
		if parseAction(in) != want {
			t.Errorf("parseAction(%q)", in)
		}
	}
	for in, want := range map[string]Action{"a": NoAction, "r": Restrict, "c": Cascade, "n": SetNull, "d": SetDefault} {
		if pgAction(in) != want {
			t.Errorf("pgAction(%q)", in)
		}
	}
	c := &Column{}
	for decl, want := range map[string]types.Name{"VARCHAR(10)": types.String, "INT": types.Integer, "NUMERIC(5,1)": types.Decimal, "DOUBLE": types.Float, "TIMESTAMP": types.DateTime} {
		*c = Column{}
		sqliteType(c, decl)
		if c.Type != want {
			t.Errorf("sqliteType(%q) = %s", decl, c.Type)
		}
	}
	*c = Column{}
	sqliteType(c, "GEOMETRY")
	if c.RawType != "GEOMETRY" {
		t.Fatal("raw fallback")
	}
	*c = Column{}
	sqliteType(c, "weird((")
	if c.RawType != "weird((" {
		t.Fatal("unparseable fallback")
	}
}

func TestNormalizePredicateCasts(t *testing.T) {
	for _, c := range [][2]string{
		{`notified_status IS DISTINCT FROM status OR notified_starts_at IS DISTINCT FROM starts_at`,
			`(((notified_status)::text IS DISTINCT FROM (status)::text) OR (notified_starts_at IS DISTINCT FROM starts_at))`},
		{`kind = 'match'`, `((kind)::text = 'match'::character varying(10))`},
		{`at > now()`, `(at > (now())::timestamp with time zone)`},
	} {
		if a, b := normalizePredicate(c[0]), normalizePredicate(c[1]); a != b {
			t.Errorf("%q and %q: %q != %q", c[0], c[1], a, b)
		}
	}
	if normalizePredicate("a = 1") == normalizePredicate("b = 1") {
		t.Error("different columns compare equal")
	}
}

func TestCompareIndexOrder(t *testing.T) {
	from, to := buildBlog(), buildBlog()
	to.Table("posts").Indexes[0].Sort = []string{"DESC"}
	for _, n := range []dialect.Name{dialect.Postgres, dialect.MySQL, dialect.SQLite, dialect.MSSQL} {
		d := cmpFor(n).Compare(from, to)
		if d.IsEmpty() || !strings.Contains(d.String(), " DESC") {
			t.Fatalf("%s: direction change not found: %s", n, d)
		}
	}
	// Only Postgres keeps the place of NULL values in an index.
	from.Table("posts").Indexes[0].Sort = []string{"DESC"}
	to.Table("posts").Indexes[0].Sort = []string{"DESC NULLS LAST"}
	if cmpFor(dialect.Postgres).Compare(from, to).IsEmpty() {
		t.Fatal("NULLS LAST differs on Postgres")
	}
	if !cmpFor(dialect.MySQL).Compare(from, to).IsEmpty() || !cmpFor(dialect.SQLite).Compare(from, to).IsEmpty() {
		t.Fatal("NULLS LAST is ignored where indexes do not keep it")
	}
}
