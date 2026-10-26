package dbtest

import (
	"context"
	"slices"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

func init() { Register(Case{"ORM/Scan", testORMScan}) }

func testORMScan(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	em := setupORM(t, db)()
	for n, books := range map[string]int{"ann": 2, "bob": 1, "cid": 0} {
		a := &ormAuthor{name: n}
		must(t, em.Persist(a))
		for range books {
			must(t, em.Persist(&ormBook{title: "t", author: a}))
		}
	}
	must(t, em.Flush(ctx))

	type count struct {
		Name  string
		Books int `db:"n"`
	}
	got, err := smorm.Scan[count](ctx, smorm.NewQuery[ormAuthor](em).LeftJoinOn(ormBook{}, "b", smorm.Raw("{b.author} = {id}")).
		Columns("{name} AS name", "count({b.id}) AS n").GroupBy("{name}").OrderBy(smorm.Raw("{name}"), smorm.Asc))
	must(t, err)
	if want := []count{{"ann", 2}, {"bob", 1}, {"cid", 0}}; !slices.Equal(got, want) {
		t.Fatalf("counts %v", got)
	}
	names, err := smorm.Scan[string](ctx, smorm.NewQuery[ormBook](em).Join("author", "a").Columns("{a.name}").Distinct())
	must(t, err)
	slices.Sort(names)
	if !slices.Equal(names, []string{"ann", "bob"}) {
		t.Fatalf("distinct names %v", names)
	}
	if _, err := smorm.Scan[count](ctx, smorm.NewQuery[ormAuthor](em).Columns("{name} AS other")); err == nil {
		t.Fatal("a column without field must fail")
	}
}
