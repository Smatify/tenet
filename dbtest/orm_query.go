package dbtest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

func init() { Register(Case{"ORM/RepositoryAndQueries", testORMQueries}) }

func names2(as []*colAuthor) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}

func testORMQueries(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, colAuthor{}, colComment{})
	em := newEM()
	goTag, sqlTag := &colTag{Label: "go"}, &colTag{Label: "sql"}
	data := map[string][]string{"ann": {"apple", "avocado"}, "bob": {"banana"}, "cid": {}, "dan": {"date", "durian"}}
	for _, n := range []string{"ann", "bob", "cid", "dan"} {
		a := &colAuthor{Name: n}
		for _, title := range data[n] {
			a.Posts.Add(&colPost{Title: title, Author: a})
		}
		if n == "ann" || n == "dan" {
			a.Tags.Add(goTag)
		}
		if n == "bob" {
			a.Tags.Add(sqlTag)
		}
		must(t, em.Persist(a))
	}
	must(t, em.Flush(ctx))
	em = newEM()
	repo := smorm.RepositoryFor[colAuthor](em)

	ann, err := repo.FindOneBy(ctx, smorm.Criteria{"Name": "ann"})
	must(t, err)
	if same, _ := repo.Find(ctx, ann.ID); same != ann || repo.EntityManager() != em {
		t.Fatal("repository Find")
	}
	if _, err := repo.FindOneBy(ctx, smorm.Criteria{"Name": "zed"}); !errors.Is(err, smorm.ErrNotFound) {
		t.Fatalf("FindOneBy missing = %v", err)
	}
	some, err := repo.FindBy(ctx, smorm.Criteria{"Name": []string{"bob", "dan"}}, smorm.OrderBy("Name", smorm.Desc))
	must(t, err)
	if !slices.Equal(names2(some), []string{"dan", "bob"}) {
		t.Fatalf("FindBy IN = %v", names2(some))
	}
	all, err := repo.FindAll(ctx, smorm.OrderBy("Name", smorm.Asc), smorm.Limit(2), smorm.Offset(1), smorm.With("Posts"))
	must(t, err)
	if !slices.Equal(names2(all), []string{"bob", "cid"}) || !all[0].Posts.IsInitialized() {
		t.Fatalf("FindAll = %v", names2(all))
	}
	if n, err := repo.Count(ctx, smorm.Criteria{"Name": []string{"ann", "cid", "x"}}); err != nil || n != 2 {
		t.Fatalf("Count = %d %v", n, err)
	}

	// Join over a collection: no duplicates, distinct count.
	q := repo.CreateQuery().Join("Posts", "p").Where(smorm.Like("p.Title", "a%"))
	withA, err := q.All(ctx)
	must(t, err)
	if !slices.Equal(names2(withA), []string{"ann"}) {
		t.Fatalf("collection join = %v", names2(withA))
	}
	if n, err := smorm.NewQuery[colAuthor](em).Join("Posts", "p").Count(ctx); err != nil || n != 3 {
		t.Fatalf("distinct count = %d %v", n, err)
	}
	if _, err := smorm.NewQuery[colAuthor](em).Join("Posts", "p").OrderBy("p.Title", smorm.Asc).All(ctx); err == nil {
		t.Fatal("ordering by a collection join must fail")
	}
	// manyToMany join.
	tagged, err := smorm.NewQuery[colAuthor](em).Join("Tags", "tg").Where(smorm.Eq("tg.Label", "go")).OrderBy("Name", smorm.Asc).All(ctx)
	must(t, err)
	if !slices.Equal(names2(tagged), []string{"ann", "dan"}) {
		t.Fatalf("m2m join = %v", names2(tagged))
	}
	// To-one join with ordering on the joined entity and association compare.
	posts, err := smorm.NewQuery[colPost](em).Join("Author", "a").
		Where(smorm.Or(smorm.Eq("a.Name", "dan"), smorm.Eq("Author", ann))).
		OrderBy("a.Name", smorm.Desc).OrderBy("Title", smorm.Asc).All(ctx)
	must(t, err)
	var titles []string
	for _, p := range posts {
		titles = append(titles, p.Title)
	}
	if !slices.Equal(titles, []string{"date", "durian", "apple", "avocado"}) {
		t.Fatalf("to-one join = %v", titles)
	}
	// Raw, Not, In over IDs, Exists.
	r, err := smorm.NewQuery[colAuthor](em).Where(smorm.Raw("LOWER({Name}) = ?", "bob"), smorm.Not(smorm.Eq("Name", "x"))).One(ctx)
	must(t, err)
	if r.Name != "bob" {
		t.Fatal("Raw")
	}
	ok, err := smorm.NewQuery[colPost](em).Where(smorm.In("ID", []int64{-1, -2})).Exists(ctx)
	must(t, err)
	if ok {
		t.Fatal("Exists")
	}
	// Iterate streams.
	var streamed []string
	for a, err := range smorm.NewQuery[colAuthor](em).OrderBy("Name", smorm.Asc).Iterate(ctx) {
		must(t, err)
		streamed = append(streamed, a.Name)
	}
	if !slices.Equal(streamed, []string{"ann", "bob", "cid", "dan"}) {
		t.Fatalf("Iterate = %v", streamed)
	}
	// Pagination.
	pq := smorm.NewQuery[colPost](em).OrderBy("Title", smorm.Asc)
	page, err := smorm.Paginate(ctx, pq, 3, 2)
	must(t, err)
	if page.Total != 5 || page.TotalPages != 3 || len(page.Items) != 1 || page.Items[0].Title != "durian" || page.HasNext() || !page.HasPrev() {
		t.Fatalf("page = %+v", page)
	}
	// Errors.
	if _, err := smorm.NewQuery[colAuthor](em).Where(smorm.Eq("Nope", 1)).All(ctx); err == nil {
		t.Fatal("unknown field")
	}
	if _, err := smorm.NewQuery[colAuthor](em).Join("Nope", "x").All(ctx); err == nil {
		t.Fatal("unknown association")
	}
}
