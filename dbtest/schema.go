package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/types"
)

func init() { Register(Case{"Schema/CreateIntrospectRoundtrip", testSchemaRoundtrip}) }

// SampleSchema returns a schema exercising every type and constraint kind
// the dialect supports. Table names start with prefix.
func SampleSchema(d dialect.Dialect, prefix string) *schema.Schema {
	n := d.Name()
	boolTrue, now := "1", "CURRENT_TIMESTAMP"
	if n == dialect.Postgres {
		boolTrue = "true"
	}
	if n == dialect.MySQL || n == dialect.MariaDB {
		now = "CURRENT_TIMESTAMP(6)"
	}
	s := schema.New()
	users := s.CreateTable(prefix + "users")
	users.Comment = "registered users"
	users.AddColumn("id", types.BigInt).SetAutoIncrement(true)
	users.AddColumn("email", types.String).SetLength(180)
	users.AddColumn("name", types.String).SetDefault("'anonymous'").SetComment("display name")
	users.AddColumn("country", types.String).SetLength(2).SetFixed(true).SetNullable(true)
	users.AddColumn("bio", types.Text).SetNullable(true)
	users.AddColumn("age", types.SmallInt).SetNullable(true)
	users.AddColumn("logins", types.Integer).SetDefault("0")
	users.AddColumn("active", types.Boolean).SetDefault(boolTrue)
	users.AddColumn("balance", types.Decimal).SetPrecision(12, 2).SetDefault("0")
	users.AddColumn("rating", types.Float).SetNullable(true)
	users.AddColumn("born", types.Date).SetNullable(true)
	users.AddColumn("wakes", types.Time).SetNullable(true)
	users.AddColumn("created", types.DateTime).SetDefault(now)
	users.AddColumn("seen", types.DateTimeTZ).SetNullable(true)
	users.AddColumn("prefs", types.JSON).SetNullable(true)
	users.AddColumn("ext_id", types.UUID).SetNullable(true)
	users.AddColumn("avatar", types.Binary).SetNullable(true)
	users.AddColumn("token", types.Binary).SetLength(16).SetNullable(true)
	users.SetPrimaryKey("id")
	users.AddUniqueIndex("email")
	users.AddIndex("country", "age")

	cats := s.CreateTable(prefix + "categories")
	cats.AddColumn("id", types.Integer).SetAutoIncrement(true)
	cats.AddColumn("parent_id", types.Integer).SetNullable(true)
	cats.SetPrimaryKey("id")
	cats.AddForeignKey([]string{"parent_id"}, cats.Name, []string{"id"})

	posts := s.CreateTable(prefix + "posts")
	posts.AddColumn("id", types.BigInt).SetAutoIncrement(true)
	posts.AddColumn("author_id", types.BigInt)
	posts.AddColumn("category_id", types.Integer).SetNullable(true)
	posts.AddColumn("title", types.String).SetLength(200)
	posts.SetPrimaryKey("id")
	posts.AddIndex("author_id").SetWhere("author_id > 0")
	posts.AddIndex("category_id")
	posts.AddForeignKey([]string{"author_id"}, users.Name, []string{"id"}).SetOnDelete(schema.Cascade)
	posts.AddForeignKey([]string{"category_id"}, cats.Name, []string{"id"}).SetOnDelete(schema.SetNull)

	tags := s.CreateTable(prefix + "post_tags")
	tags.AddColumn("post_id", types.BigInt)
	tags.AddColumn("tag", types.String).SetLength(50)
	tags.SetPrimaryKey("post_id", "tag")
	tags.AddForeignKey([]string{"post_id"}, posts.Name, []string{"id"}).SetOnDelete(schema.Cascade).SetOnUpdate(schema.Restrict)

	if d.Features().Has(dialect.FeatureSequences) {
		s.CreateSequence(prefix+"invoice_seq", 1000, 5)
	}
	return s
}

// ApplySchema executes statements; it fails the test on error.
func ApplySchema(t *testing.T, db dbal.IDB, stmts []string) {
	t.Helper()
	for _, q := range stmts {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s\n-> %v", q, err)
		}
	}
}

// DropSchemaQuietly drops s ignoring errors (for setup and cleanup).
func DropSchemaQuietly(db dbal.IDB, s *schema.Schema) {
	for _, q := range schema.NewDDL(db.Dialect()).DropSchema(s) {
		_, _ = db.ExecContext(context.Background(), q)
	}
}

func onlyPrefix(prefix string) func(string) bool {
	return func(name string) bool { return !strings.HasPrefix(strings.ToLower(name), prefix) }
}

func testSchemaRoundtrip(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	const prefix = "sch_"
	want := SampleSchema(d, prefix)
	if err := want.Validate(); err != nil {
		t.Fatal(err)
	}
	DropSchemaQuietly(db, want)
	t.Cleanup(func() { DropSchemaQuietly(db, want) })

	stmts, err := schema.NewDDL(d).CreateSchema(want)
	if err != nil {
		t.Fatal(err)
	}
	ApplySchema(t, db, stmts)

	got, err := schema.Introspect(ctx, db, d)
	if err != nil {
		t.Fatal(err)
	}
	cmp := schema.NewComparator(d).IgnoreTables(onlyPrefix(prefix))
	diff := cmp.Compare(got, want)
	if !diff.IsEmpty() {
		t.Fatalf("schema differs after roundtrip:\n%s", DescribeDiff(diff))
	}
	// Sanity checks on details the comparator normalizes.
	u := got.Table(prefix + "users")
	if u == nil || !u.Column("id").AutoIncrement || u.Column("email").Nullable || !u.Column("bio").Nullable {
		t.Fatalf("users = %+v", u)
	}
	if fk := got.Table(prefix + "posts").ForeignKey(schema.IdentifierName("fk", prefix+"posts", []string{"author_id"})); fk == nil || fk.OnDelete != schema.Cascade {
		t.Fatalf("fk = %+v", fk)
	}
	if tg := got.Table(prefix + "post_tags"); tg.PrimaryKey == nil || len(tg.PrimaryKey.Columns) != 2 {
		t.Fatalf("composite pk = %+v", tg.PrimaryKey)
	}
	if d.Features().Has(dialect.FeatureSequences) {
		if s := got.Sequence(prefix + "invoice_seq"); s == nil || s.Start != 1000 || s.Increment != 5 {
			t.Fatalf("sequence = %+v", s)
		}
	}
	// A real change must be detected.
	changed := want.Clone()
	changed.Table(prefix + "users").Column("email").SetLength(200)
	if cmp.Compare(got, changed).IsEmpty() {
		t.Fatal("length change not detected")
	}
}

// DescribeDiff renders a diff for test failure messages.
func DescribeDiff(d *schema.Diff) string {
	var b strings.Builder
	for _, t := range d.CreateTables {
		b.WriteString("create table " + t.Name + "\n")
	}
	for _, t := range d.DropTables {
		b.WriteString("drop table " + t.Name + "\n")
	}
	for _, s := range d.CreateSequences {
		b.WriteString("create sequence " + s.Name + "\n")
	}
	for _, s := range d.DropSequences {
		b.WriteString("drop sequence " + s.Name + "\n")
	}
	for _, td := range d.AlterTables {
		b.WriteString("alter table " + td.To.Name + "\n")
		for _, c := range td.AddColumns {
			b.WriteString("  add column " + c.Name + "\n")
		}
		for _, c := range td.DropColumns {
			b.WriteString("  drop column " + c.Name + "\n")
		}
		for _, c := range td.ChangeColumns {
			b.WriteString("  change column " + c.To.Name + ":")
			if c.Type {
				b.WriteString(" type(" + string(c.From.Type) + c.From.RawType + "->" + string(c.To.Type) + ")")
			}
			if c.Nullable {
				b.WriteString(" nullable")
			}
			if c.Default {
				from, to := "<nil>", "<nil>"
				if c.From.Default != nil {
					from = *c.From.Default
				}
				if c.To.Default != nil {
					to = *c.To.Default
				}
				b.WriteString(" default(" + from + " -> " + to + ")")
			}
			if c.AutoIncrement {
				b.WriteString(" autoincrement")
			}
			if c.Comment {
				b.WriteString(" comment(" + c.From.Comment + " -> " + c.To.Comment + ")")
			}
			b.WriteString("\n")
		}
		for _, i := range td.AddIndexes {
			b.WriteString("  add index " + i.Name + " (" + strings.Join(i.Columns, ",") + ") where " + i.Where + "\n")
		}
		for _, i := range td.DropIndexes {
			b.WriteString("  drop index " + i.Name + " (" + strings.Join(i.Columns, ",") + ") where " + i.Where + "\n")
		}
		for _, f := range td.AddForeignKeys {
			b.WriteString("  add fk " + f.Name + " " + string(f.OnDelete) + "/" + string(f.OnUpdate) + "\n")
		}
		for _, f := range td.DropForeignKeys {
			b.WriteString("  drop fk " + f.Name + " " + string(f.OnDelete) + "/" + string(f.OnUpdate) + "\n")
		}
		if td.PrimaryKeyChanged {
			b.WriteString("  primary key changed\n")
		}
		if td.CommentChanged {
			b.WriteString("  comment changed: " + td.From.Comment + " -> " + td.To.Comment + "\n")
		}
	}
	return b.String()
}
