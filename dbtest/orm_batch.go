package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/smorm/dbal"
)

func init() { Register(Case{"ORM/BatchInsert", testORMBatch}) }

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
