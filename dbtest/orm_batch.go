package dbtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
)

func init() {
	Register(Case{"ORM/BatchInsert", testORMBatch}, Case{"ORM/BatchInsertIdentity", testORMBatchIdentity},
		Case{"ORM/BatchUpdate", testORMBatchUpdate})
}

// Entities with identity identifiers are inserted with one statement on
// Postgres, which returns the identifiers in the order of the rows.
func testORMBatchIdentity(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	cats := make([]*ormCategory, 600) // more than one chunk
	for i := range cats {
		cats[i] = &ormCategory{Name: fmt.Sprint("c", i)}
		must(t, em.Persist(cats[i]))
	}
	statements := 0
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "INSERT" && strings.Contains(ev.Query, "orm_category") {
			statements++
		}
	}})
	must(t, em.Flush(ctx))
	if want := map[bool]int{true: 2, false: 600}[db.Dialect().Name() == dialect.Postgres]; statements != want {
		t.Fatalf("statements = %d, want %d", statements, want)
	}
	for i, c := range cats {
		var name string
		must(t, db.NewSelect().Column("name").Table("orm_category").Where("id = ?", c.ID).Scan(ctx, &name))
		if name != fmt.Sprint("c", i) || (i > 0 && c.ID <= cats[i-1].ID) {
			t.Fatalf("row %d: id %d has name %q", i, c.ID, name)
		}
	}
}

// Updates of the same columns are written with one statement per chunk on
// Postgres; unchanged entities are left out.
func testORMBatchUpdate(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	cats := make([]*ormCategory, 600) // more than one chunk
	for i := range cats {
		cats[i] = &ormCategory{Name: fmt.Sprint("c", i)}
		must(t, em.Persist(cats[i]))
	}
	must(t, em.Flush(ctx))
	for i, c := range cats {
		if i != 300 {
			c.Name = fmt.Sprint("u", i)
		}
	}
	statements := 0
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "UPDATE" && strings.Contains(ev.Query, "orm_category") {
			statements++
		}
	}})
	must(t, em.Flush(ctx))
	if want := map[bool]int{true: 2, false: 599}[db.Dialect().Name() == dialect.Postgres]; statements != want {
		t.Fatalf("statements = %d, want %d", statements, want)
	}
	for i, c := range cats {
		var name string
		must(t, db.NewSelect().Column("name").Table("orm_category").Where("id = ?", c.ID).Scan(ctx, &name))
		want := fmt.Sprint("u", i)
		if i == 300 {
			want = "c300"
		}
		if name != want {
			t.Fatalf("row %d: name %q, want %q", i, name, want)
		}
	}
}

func testORMBatch(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	a := &ormAuthor{name: "batch"}
	must(t, em.Persist(a))
	for i := range 1203 {
		must(t, em.Persist(&ormBook{title: "b" + string(rune('a'+i%26)), author: a}))
	}
	statements := 0
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "INSERT" && strings.Contains(ev.Query, "orm_book") {
			statements++
		}
	}})
	must(t, em.Flush(ctx))
	if n := Count(t, db, "orm_book"); n != 1203 {
		t.Fatalf("rows = %d", n)
	}
	if statements != 3 { // 4 columns -> 500 rows per statement
		t.Fatalf("statements = %d", statements)
	}
}
