package dbtest

import (
	"context"
	"testing"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/types"
)

func init() { Register(Case{"Schema/MigrateUpAndDown", testSchemaMigrate}) }

func migrateV1(d dialect.Dialect, p string) *schema.Schema {
	s := schema.New()
	u := s.CreateTable(p + "users")
	u.AddColumn("id", types.BigInt).SetAutoIncrement(true)
	u.AddColumn("email", types.String).SetLength(100)
	u.AddColumn("nick", types.String).SetLength(30)
	u.AddColumn("legacy", types.Integer).SetNullable(true).SetDefault("7")
	u.AddColumn("status", types.String).SetLength(10).SetDefault("'new'")
	u.SetPrimaryKey("id")
	u.AddUniqueIndex("email")
	u.AddIndex("nick")

	p1 := s.CreateTable(p + "posts")
	p1.AddColumn("id", types.BigInt).SetAutoIncrement(true)
	p1.AddColumn("author_id", types.BigInt)
	p1.AddColumn("title", types.String).SetLength(50)
	p1.SetPrimaryKey("id")
	p1.AddIndex("author_id")
	p1.AddForeignKey([]string{"author_id"}, u.Name, []string{"id"})

	old := s.CreateTable(p + "old")
	old.AddColumn("id", types.Integer)
	old.SetPrimaryKey("id")

	pk := s.CreateTable(p + "pairs")
	pk.AddColumn("a", types.Integer)
	pk.AddColumn("b", types.Integer)
	pk.SetPrimaryKey("a")
	if d.Features().Has(dialect.FeatureSequences) {
		s.CreateSequence(p+"seq_a", 1, 1)
	}
	return s
}

func migrateV2(d dialect.Dialect, p string) *schema.Schema {
	s := migrateV1(d, p)
	u := s.Table(p + "users")
	u.Comment = "people"
	u.Column("email").SetLength(150)          // widen
	u.Column("nick").SetNullable(true)        // relax
	u.Column("status").SetDefault("'active'") // new default
	u.DropColumn("legacy")                    // drop
	u.AddColumn("bio", types.Text).SetNullable(true)
	u.AddColumn("score", types.Integer).SetDefault("0") // NOT NULL with default
	u.Indexes = nil
	u.AddUniqueIndex("email").SetName("uniq_email_v2")
	u.AddIndex("status")

	posts := s.Table(p + "posts")
	posts.ForeignKeys[0].SetOnDelete(schema.Cascade)
	posts.AddColumn("views", types.BigInt).SetNullable(true)

	s.DropTable(p + "old")
	tags := s.CreateTable(p + "tags")
	tags.AddColumn("id", types.Integer).SetAutoIncrement(true)
	tags.AddColumn("post_id", types.BigInt)
	tags.SetPrimaryKey("id")
	tags.AddForeignKey([]string{"post_id"}, posts.Name, []string{"id"}).SetOnDelete(schema.Cascade)

	s.Table(p+"pairs").SetPrimaryKey("a", "b")
	if d.Features().Has(dialect.FeatureSequences) {
		s.Sequences = nil
		s.CreateSequence(p+"seq_b", 10, 2)
	}
	return s
}

func testSchemaMigrate(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	const p = "mig_"
	v1, v2 := migrateV1(d, p), migrateV2(d, p)
	for _, s := range []*schema.Schema{v2, v1} {
		DropSchemaQuietly(db, s)
	}
	t.Cleanup(func() {
		DropSchemaQuietly(db, v2)
		DropSchemaQuietly(db, v1)
	})
	ddl := schema.NewDDL(d)
	stmts, err := ddl.CreateSchema(v1)
	if err != nil {
		t.Fatal(err)
	}
	ApplySchema(t, db, stmts)

	// Seed data that must survive the migration.
	if _, err := db.NewInsert().Table(p+"users").Columns("email", "nick", "status").
		Values("a@x", "ann", "new").Values("b@x", "bob", "new").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var uid int64
	if err := db.NewSelect().Column("id").Table(p+"users").Where("email = ?", "a@x").Scan(ctx, &uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewInsert().Table(p+"posts").Columns("author_id", "title").Values(uid, "hello").Exec(ctx); err != nil {
		t.Fatal(err)
	}

	migrate := func(target *schema.Schema) {
		t.Helper()
		current, err := schema.Introspect(ctx, db, d)
		if err != nil {
			t.Fatal(err)
		}
		stmts, err := ddl.MigrateSchemas(current, target, onlyPrefix(p))
		if err != nil {
			t.Fatal(err)
		}
		if len(stmts) == 0 {
			t.Fatal("expected migration statements")
		}
		ApplySchema(t, db, stmts)
		after, err := schema.Introspect(ctx, db, d)
		if err != nil {
			t.Fatal(err)
		}
		if diff := schema.NewComparator(d).IgnoreTables(onlyPrefix(p)).Compare(after, target); !diff.IsEmpty() {
			t.Fatalf("schema differs after migration:\n%s\nstatements:\n%v", DescribeDiff(diff), stmts)
		}
	}

	migrate(v2)
	if n := Count(t, db, p+"users"); n != 2 {
		t.Fatalf("users after up = %d", n)
	}
	var title string
	if err := db.NewSelect().Column("title").Table(p+"posts").Where("author_id = ?", uid).Scan(ctx, &title); err != nil || title != "hello" {
		t.Fatalf("post after up = %q, %v", title, err)
	}
	var score int
	if err := db.NewSelect().Column("score").Table(p+"users").Where("id = ?", uid).Scan(ctx, &score); err != nil || score != 0 {
		t.Fatalf("score default = %d, %v", score, err)
	}
	// ON DELETE CASCADE is active after the migration.
	if _, err := db.NewDelete().Table(p+"users").Where("id = ?", uid).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n := Count(t, db, p+"posts"); n != 0 {
		t.Fatalf("cascade did not delete posts: %d", n)
	}

	migrate(v1)
	if n := Count(t, db, p+"users"); n != 1 {
		t.Fatalf("users after down = %d", n)
	}
}
