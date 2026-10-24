package dbtest

import (
	"context"
	"slices"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/uuid"
)

func init() { Register(Case{"ORM/GeneratedColumn", testORMGenerated}) }

type ormNumbered struct {
	smorm.Entity `smorm:"table:orm_numbered"`
	smorm.UUIDID
	number int64  `smorm:"generated:identity"`
	title  string `smorm:"length:50"`
}

// testORMGenerated adds a generated:identity column to a table with rows,
// then inserts through the entity manager, which reads the number back.
func testORMGenerated(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	if d.Name() == dialect.SQLite {
		t.Skip("SQLite generates values only for the primary key")
	}
	reg := mapping.NewRegistry()
	must(t, reg.Register(ormNumbered{}))
	target := mapping.BuildSchema(reg.All())
	before := mapping.BuildSchema(reg.All())
	tbl := before.Table("orm_numbered")
	tbl.DropColumn("number")
	tbl.Indexes = slices.DeleteFunc(tbl.Indexes, func(i *schema.Index) bool { return i.Columns[0] == "number" })
	DropSchemaQuietly(db, target)
	t.Cleanup(func() { DropSchemaQuietly(db, target) })
	ddl := schema.NewDDL(d)
	stmts, err := ddl.CreateSchema(before)
	must(t, err)
	ApplySchema(t, db, stmts)
	if _, err := db.NewInsert().Table("orm_numbered").Columns("id", "title").Values(uuid.NewV7(), "old").Exec(ctx); err != nil {
		t.Fatal(err)
	}

	only := func(name string) bool { return name != "orm_numbered" }
	current, err := schema.Introspect(ctx, db, d)
	must(t, err)
	stmts, err = ddl.MigrateSchemas(current, target, only)
	must(t, err)
	ApplySchema(t, db, stmts)
	var n int64
	must(t, db.NewSelect().Column("number").Table("orm_numbered").Scan(ctx, &n))
	if n != 1 {
		t.Fatalf("existing row number = %d", n)
	}

	em := smorm.NewEntityManager(db, smorm.WithRegistry(reg))
	a, b := &ormNumbered{title: "a"}, &ormNumbered{title: "b"}
	must(t, em.Persist(a))
	must(t, em.Persist(b))
	must(t, em.Flush(ctx))
	if a.number != 2 || b.number != 3 {
		t.Fatalf("numbers = %d, %d", a.number, b.number)
	}
	a.title = "a2" // change tracking knows the generated value
	must(t, em.Flush(ctx))
	got, err := smorm.Find[ormNumbered](ctx, smorm.NewEntityManager(db, smorm.WithRegistry(reg)), a.ID())
	must(t, err)
	if got.number != 2 || got.title != "a2" {
		t.Fatalf("reloaded = %d %q", got.number, got.title)
	}

	stable := func() {
		t.Helper()
		after, err := schema.Introspect(ctx, db, d)
		must(t, err)
		if diff := schema.NewComparator(d).IgnoreTables(only).Compare(after, target); !diff.IsEmpty() {
			t.Fatalf("schema differs after migration:\n%s\nstatements:\n%v", DescribeDiff(diff), stmts)
		}
	}
	stable()

	// A new table numbers from 1.
	DropSchemaQuietly(db, target)
	stmts, err = ddl.CreateSchema(target)
	must(t, err)
	ApplySchema(t, db, stmts)
	stable()
	c := &ormNumbered{title: "c"}
	must(t, em.Persist(c))
	must(t, em.Flush(ctx))
	if c.number != 1 {
		t.Fatalf("number in new table = %d", c.number)
	}
	if d.Name() == dialect.MSSQL {
		return // SQL Server cannot turn an existing column into an identity
	}

	// An existing number column becomes generated and continues after the
	// highest number.
	DropSchemaQuietly(db, target)
	plain := mapping.BuildSchema(reg.All())
	plain.Table("orm_numbered").Column("number").SetAutoIncrement(false)
	stmts, err = ddl.CreateSchema(plain)
	must(t, err)
	ApplySchema(t, db, stmts)
	if _, err := db.NewInsert().Table("orm_numbered").Columns("id", "number", "title").Values(uuid.NewV7(), 41, "old").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	current, err = schema.Introspect(ctx, db, d)
	must(t, err)
	stmts, err = ddl.MigrateSchemas(current, target, only)
	must(t, err)
	ApplySchema(t, db, stmts)
	stable()
	e := &ormNumbered{title: "e"}
	must(t, em.Persist(e))
	must(t, em.Flush(ctx))
	if e.number != 42 {
		t.Fatalf("number after conversion = %d", e.number)
	}
}
