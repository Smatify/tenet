package orm

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/entity"
)

type wDoc struct {
	entity.Header `smorm:"table:wd"`
	ID            int64
	Title         string
	Views         int64
	Version       int64 `smorm:"version"`
}

func TestWriteFields(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.Postgres)
	if err := em.reg.Register(wDoc{}); err != nil {
		t.Fatal(err)
	}
	rec.Returns(`FROM "wd"`, []string{"id", "title", "views", "version"}, []driver.Value{int64(1), "a", int64(3), int64(7)})
	p, err := em.Find(ctx, typeOf[wDoc](), int64(1))
	if err != nil {
		t.Fatal(err)
	}
	d := p.(*wDoc)
	d.Views, d.Title = 4, "b"
	rec.Reset()
	if err := em.WriteFields(ctx, d, "Views"); err != nil {
		t.Fatal(err)
	}
	q := rec.Queries()
	if len(q) != 1 || !strings.Contains(q[0], `UPDATE "wd" SET "views" = $1 WHERE ("id" = $2)`) || strings.Contains(q[0], "version") {
		t.Fatalf("WriteFields wrote %v", q)
	}
	// The next flush writes the other change with the version check, not
	// the written field again.
	rec.Reset()
	rec.Returns("UPDATE", nil)
	if err := em.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	flush := strings.Join(rec.Queries(), "\n")
	if !strings.Contains(flush, `"title"`) || strings.Contains(flush, `"views"`) || !strings.Contains(flush, `"version"`) {
		t.Fatalf("flush after WriteFields: %s", flush)
	}
	for _, bad := range []string{"Version", "ID", "Nope"} {
		if err := em.WriteFields(ctx, d, bad); err == nil {
			t.Errorf("WriteFields(%s) accepted", bad)
		}
	}
	if err := em.WriteFields(ctx, &wDoc{}, "Views"); err == nil {
		t.Error("WriteFields on a new entity")
	}
}

func TestQueryWithoutFilterAndRawOrder(t *testing.T) {
	em, _ := newUnitEM(t, dialect.Postgres)
	_ = em.reg.Register(qAuthor{})
	em.Filters().Enable("tenant", ColumnFilter("name", "acme"))
	if s := sqlOf(t, em.NewQuery(typeOf[qAuthor]()).WithoutFilter("tenant")); strings.Contains(s, `"name" =`) {
		t.Fatalf("WithoutFilter: %s", s)
	}
	if s := sqlOf(t, em.NewQuery(typeOf[qAuthor]())); !strings.Contains(s, `"name" =`) {
		t.Fatalf("filter must stay on for other queries: %s", s)
	}
	s := sqlOf(t, em.NewQuery(typeOf[qAuthor]()).OrderBy(Raw("LOWER({Name})"), "DESC").OrderBy("ID", "ASC"))
	if !strings.Contains(s, `ORDER BY LOWER("t0"."name") DESC, "t0"."id" ASC`) {
		t.Fatalf("raw order: %s", s)
	}
	if _, _, err := em.NewQuery(typeOf[qAuthor]()).OrderBy(Raw("{Nope}"), "ASC").SQL(); err == nil {
		t.Fatal("unknown field in raw order")
	}
}
