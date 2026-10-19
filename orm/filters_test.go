package orm

import (
	"strings"
	"testing"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/mapping"
)

func TestFilters(t *testing.T) {
	em, _ := newUnitEM(t, dialect.Postgres)
	_ = em.reg.Register(qAuthor{})
	f := em.Filters()
	if !f.IsEnabled(SoftDeleteFilterName) {
		t.Fatal("soft delete filter is on by default")
	}
	f.Enable("tenant", ColumnFilter("name", "acme"))
	f.Enable("tenant", ColumnFilter("name", "acme")) // replace keeps order
	f.Enable("never", FilterFunc(func(*mapping.Entity, func(string) dbal.Ident) (dbal.Expr, bool) { return nil, false }))
	if strings.Join(f.Enabled(), ",") != "softdelete,tenant,never" {
		t.Fatalf("enabled = %v", f.Enabled())
	}
	s := sqlOf(t, em.NewQuery(typeOf[qAuthor]()).Join("Books", "b"))
	if !strings.Contains(s, `"t0"."name" = $1`) {
		t.Fatalf("root filter: %s", s)
	}
	s = sqlOf(t, em.NewQuery(typeOf[qBook]()).LeftJoin("Author", "a"))
	if !strings.Contains(s, `ON "a"."id" = "t0"."author_id" AND "a"."name" = $1`) {
		t.Fatalf("join filter: %s", s)
	}
	f.Disable("tenant")
	if f.IsEnabled("tenant") || strings.Contains(sqlOf(t, em.NewQuery(typeOf[qAuthor]())), "name\" =") {
		t.Fatal("Disable")
	}
}
