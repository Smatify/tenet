package tenet_test

import (
	"context"
	"fmt"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
)

// These examples show the calls; the same code runs with its output in the
// examples of github.com/smatify/tenet/dialect/sqlitedialect.

// Author and Post are entities in the Go style: exported fields, a
// collection on the inverse side, the owning side a pointer. Entities made
// with make:entity use the Doctrine style instead: unexported fields and
// generated getters.
type Author struct {
	tenet.Entity `tenet:"table:author"`
	ID           int64
	Name         string                 `tenet:"length:100"`
	Posts        tenet.Collection[Post] `tenet:"oneToMany,mappedBy:Author,cascade:persist,orderBy:Title"`
}

type Post struct {
	tenet.Entity `tenet:"table:post"`
	ID           int64
	Title        string  `tenet:"length:200"`
	Views        int     `tenet:"default:0"`
	Author       *Author `tenet:"inversedBy:Posts,notnull"`
}

// db is opened by the application, e.g.
// dbal.Open("pgx", dsn, pgdialect.New()).
var db *dbal.DB

func ExampleNewEntityManager() {
	ctx := context.Background()
	// One entity manager per request or job: it is not safe for concurrent use.
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))

	ann := &Author{Name: "Ann"}
	ann.Posts.Add(&Post{Title: "Hello", Author: ann})
	if err := em.Persist(ann); err != nil { // the post follows (cascade:persist)
		panic(err)
	}
	if err := em.Flush(ctx); err != nil { // one transaction, the author first
		panic(err)
	}
	fmt.Println(ann.ID != 0) // the database numbered it
}

func ExampleFind() {
	ctx := context.Background()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	author, err := tenet.Find[Author](ctx, em, int64(1))
	if err != nil {
		panic(err) // tenet.ErrNotFound when there is none
	}
	again, _ := tenet.Find[Author](ctx, em, int64(1))
	fmt.Println(author == again)                 // the identity map: one instance per row
	fmt.Println(author.Name, author.Posts.Len()) // the posts load when used
}

func ExampleNewQuery() {
	ctx := context.Background()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	// Fields are named as in Go (Doctrine style entities: their unexported
	// names) or with the generated field refs.
	posts, err := tenet.NewQuery[Post](em).As("p").Join("p.Author", "a").
		Where(tenet.Like("a.Name", "A%"), tenet.Gt("p.Views", 10)).
		OrderBy("p.Title", tenet.Asc).
		With("Author"). // loaded with the posts, no query per author
		All(ctx)
	if err != nil {
		panic(err)
	}
	for _, p := range posts {
		fmt.Println(p.Title, "by", p.Author.Name)
	}
}

func ExamplePaginate() {
	ctx := context.Background()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	page, err := tenet.Paginate(ctx, tenet.NewQuery[Post](em).OrderBy("Title", tenet.Asc), 2, 25)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(page.Items), "of", page.Total, "more:", page.HasNext())
}

func ExampleEntityManager_Transactional() {
	ctx := context.Background()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	// fn and the final flush run in one transaction: an error rolls back both.
	err := em.Transactional(ctx, func(ctx context.Context, em *tenet.EntityManager) error {
		return em.Persist(&Author{Name: "Ann"})
	})
	if err != nil {
		panic(err)
	}
}

func ExampleUpdate() {
	ctx := context.Background()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	// One statement for many rows, past the unit of work: loaded entities
	// keep their values.
	n, err := tenet.Update[Post](em).Set("Views", tenet.Raw("{Views} + 1")).Where(tenet.Eq("Author", int64(1))).Exec(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(n, "posts updated")
}

func ExampleScan() {
	ctx := context.Background()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	type views struct {
		Author int64
		Total  int
	}
	rows, err := tenet.Scan[views](ctx, tenet.NewQuery[Post](em).
		Columns("{Author} AS author", "sum({Views}) AS total").GroupBy("{Author}"))
	if err != nil {
		panic(err)
	}
	fmt.Println(rows)
}
