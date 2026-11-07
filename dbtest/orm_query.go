package dbtest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
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
	repo := tenet.RepositoryFor[colAuthor](em)

	ann, err := repo.FindOneBy(ctx, tenet.Criteria{"Name": "ann"})
	must(t, err)
	if same, _ := repo.Find(ctx, ann.ID); same != ann || repo.EntityManager() != em {
		t.Fatal("repository Find")
	}
	if _, err := repo.FindOneBy(ctx, tenet.Criteria{"Name": "zed"}); !errors.Is(err, tenet.ErrNotFound) {
		t.Fatalf("FindOneBy missing = %v", err)
	}
	some, err := repo.FindBy(ctx, tenet.Criteria{"Name": []string{"bob", "dan"}}, tenet.OrderBy("Name", tenet.Desc))
	must(t, err)
	if !slices.Equal(names2(some), []string{"dan", "bob"}) {
		t.Fatalf("FindBy IN = %v", names2(some))
	}
	all, err := repo.FindAll(ctx, tenet.OrderBy("Name", tenet.Asc), tenet.Limit(2), tenet.Offset(1), tenet.With("Posts"))
	must(t, err)
	if !slices.Equal(names2(all), []string{"bob", "cid"}) || !all[0].Posts.IsInitialized() {
		t.Fatalf("FindAll = %v", names2(all))
	}
	if n, err := repo.Count(ctx, tenet.Criteria{"Name": []string{"ann", "cid", "x"}}); err != nil || n != 2 {
		t.Fatalf("Count = %d %v", n, err)
	}

	// Join over a collection: no duplicates, distinct count.
	q := repo.CreateQuery().Join("Posts", "p").Where(tenet.Like("p.Title", "a%"))
	withA, err := q.All(ctx)
	must(t, err)
	if !slices.Equal(names2(withA), []string{"ann"}) {
		t.Fatalf("collection join = %v", names2(withA))
	}
	if n, err := tenet.NewQuery[colAuthor](em).Join("Posts", "p").Count(ctx); err != nil || n != 3 {
		t.Fatalf("distinct count = %d %v", n, err)
	}
	if _, err := tenet.NewQuery[colAuthor](em).Join("Posts", "p").OrderBy("p.Title", tenet.Asc).All(ctx); err == nil {
		t.Fatal("ordering by a collection join must fail")
	}
	// manyToMany join.
	tagged, err := tenet.NewQuery[colAuthor](em).Join("Tags", "tg").Where(tenet.Eq("tg.Label", "go")).OrderBy("Name", tenet.Asc).All(ctx)
	must(t, err)
	if !slices.Equal(names2(tagged), []string{"ann", "dan"}) {
		t.Fatalf("m2m join = %v", names2(tagged))
	}
	// To-one join with ordering on the joined entity and association compare.
	posts, err := tenet.NewQuery[colPost](em).Join("Author", "a").
		Where(tenet.Or(tenet.Eq("a.Name", "dan"), tenet.Eq("Author", ann))).
		OrderBy("a.Name", tenet.Desc).OrderBy("Title", tenet.Asc).All(ctx)
	must(t, err)
	var titles []string
	for _, p := range posts {
		titles = append(titles, p.Title)
	}
	if !slices.Equal(titles, []string{"date", "durian", "apple", "avocado"}) {
		t.Fatalf("to-one join = %v", titles)
	}
	// Raw, Not, In over IDs, Exists.
	r, err := tenet.NewQuery[colAuthor](em).Where(tenet.Raw("LOWER({Name}) = ?", "bob"), tenet.Not(tenet.Eq("Name", "x"))).One(ctx)
	must(t, err)
	if r.Name != "bob" {
		t.Fatal("Raw")
	}
	ok, err := tenet.NewQuery[colPost](em).Where(tenet.In("ID", []int64{-1, -2})).Exists(ctx)
	must(t, err)
	if ok {
		t.Fatal("Exists")
	}
	// Iterate streams.
	var streamed []string
	for a, err := range tenet.NewQuery[colAuthor](em).OrderBy("Name", tenet.Asc).Iterate(ctx) {
		must(t, err)
		streamed = append(streamed, a.Name)
	}
	if !slices.Equal(streamed, []string{"ann", "bob", "cid", "dan"}) {
		t.Fatalf("Iterate = %v", streamed)
	}
	// Pagination.
	pq := tenet.NewQuery[colPost](em).OrderBy("Title", tenet.Asc)
	page, err := tenet.Paginate(ctx, pq, 3, 2)
	must(t, err)
	if page.Total != 5 || page.TotalPages != 3 || len(page.Items) != 1 || page.Items[0].Title != "durian" || page.HasNext() || !page.HasPrev() {
		t.Fatalf("page = %+v", page)
	}
	// Errors.
	if _, err := tenet.NewQuery[colAuthor](em).Where(tenet.Eq("Nope", 1)).All(ctx); err == nil {
		t.Fatal("unknown field")
	}
	if _, err := tenet.NewQuery[colAuthor](em).Join("Nope", "x").All(ctx); err == nil {
		t.Fatal("unknown association")
	}
}
