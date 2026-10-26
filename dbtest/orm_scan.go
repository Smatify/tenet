package dbtest

import (
	"context"
	"slices"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
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

func init() { Register(Case{"ORM/JoinSub", testORMJoinSub}) }

func testORMJoinSub(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	em := setupORM(t, db)()
	for n, titles := range map[string][]string{"ann": {"a1", "a2"}, "bob": {"b1"}, "cid": nil} {
		a := &ormAuthor{name: n}
		must(t, em.Persist(a))
		for _, title := range titles {
			must(t, em.Persist(&ormBook{title: title, author: a}))
		}
	}
	must(t, em.Flush(ctx))
	type row struct {
		Name  string
		Books int
		Last  *string
	}
	counts := smorm.NewQuery[ormBook](em).Columns("{author} AS author_id", "count(*) AS n").GroupBy("{author}")
	last := smorm.NewQuery[ormBook](em).Columns("{title} AS title").Where(smorm.Raw("{author} = a.id")).OrderBy("title", smorm.Desc).Limit(1)
	q := smorm.NewQuery[ormAuthor](em).As("a").LeftJoinSub(counts.Sub(), "n", smorm.Raw("n.author_id = {a.id}")).
		LeftJoinLateral(last.Sub(), "l").Columns("{a.name} AS name", "COALESCE(n.n, 0) AS books", "l.title AS last").OrderBy("a.name", smorm.Asc)
	got, err := smorm.Scan[row](ctx, q)
	if db.Dialect().Name() != dialect.Postgres {
		if err == nil {
			t.Fatal("JOIN LATERAL must fail on " + string(db.Dialect().Name()))
		}
		return
	}
	must(t, err)
	if len(got) != 3 || got[0].Books != 2 || *got[0].Last != "a2" || got[1].Books != 1 || got[2].Books != 0 || got[2].Last != nil {
		t.Fatalf("rows %+v", got)
	}
}
