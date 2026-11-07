package smorm_test

import (
	"context"
	"fmt"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

// These examples show the calls; the same code runs with its output in the
// examples of github.com/smatify/smorm/dialect/sqlitedialect.

// Author and Post are entities in the Go style: exported fields, a
// collection on the inverse side, the owning side a pointer. Entities made
// with make:entity use the Doctrine style instead: unexported fields and
// generated getters.
type Author struct {
	smorm.Entity `smorm:"table:author"`
	ID           int64
	Name         string                 `smorm:"length:100"`
	Posts        smorm.Collection[Post] `smorm:"oneToMany,mappedBy:Author,cascade:persist,orderBy:Title"`
}

type Post struct {
	smorm.Entity `smorm:"table:post"`
	ID           int64
	Title        string  `smorm:"length:200"`
	Views        int     `smorm:"default:0"`
	Author       *Author `smorm:"inversedBy:Posts,notnull"`
}

// db is opened by the application, e.g.
// dbal.Open("pgx", dsn, pgdialect.New()).
var db *dbal.DB

func ExampleNewEntityManager() {
	ctx := context.Background()
	// One entity manager per request or job: it is not safe for concurrent use.
	em := smorm.NewEntityManager(db, smorm.WithContext(ctx))

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
	em := smorm.NewEntityManager(db, smorm.WithContext(ctx))
	author, err := smorm.Find[Author](ctx, em, int64(1))
	if err != nil {
		panic(err) // smorm.ErrNotFound when there is none
	}
	again, _ := smorm.Find[Author](ctx, em, int64(1))
	fmt.Println(author == again)                 // the identity map: one instance per row
	fmt.Println(author.Name, author.Posts.Len()) // the posts load when used
}

func ExampleNewQuery() {
	ctx := context.Background()
	em := smorm.NewEntityManager(db, smorm.WithContext(ctx))
	// Fields are named as in Go (Doctrine style entities: their unexported
	// names) or with the generated field refs.
	posts, err := smorm.NewQuery[Post](em).As("p").Join("p.Author", "a").
		Where(smorm.Like("a.Name", "A%"), smorm.Gt("p.Views", 10)).
		OrderBy("p.Title", smorm.Asc).
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
	em := smorm.NewEntityManager(db, smorm.WithContext(ctx))
	page, err := smorm.Paginate(ctx, smorm.NewQuery[Post](em).OrderBy("Title", smorm.Asc), 2, 25)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(page.Items), "of", page.Total, "more:", page.HasNext())
}

func ExampleEntityManager_Transactional() {
	ctx := context.Background()
	em := smorm.NewEntityManager(db, smorm.WithContext(ctx))
	// fn and the final flush run in one transaction: an error rolls back both.
	err := em.Transactional(ctx, func(ctx context.Context, em *smorm.EntityManager) error {
		return em.Persist(&Author{Name: "Ann"})
	})
	if err != nil {
		panic(err)
	}
}

func ExampleUpdate() {
	ctx := context.Background()
	em := smorm.NewEntityManager(db, smorm.WithContext(ctx))
	// One statement for many rows, past the unit of work: loaded entities
	// keep their values.
	n, err := smorm.Update[Post](em).Set("Views", smorm.Raw("{Views} + 1")).Where(smorm.Eq("Author", int64(1))).Exec(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(n, "posts updated")
}

func ExampleScan() {
	ctx := context.Background()
	em := smorm.NewEntityManager(db, smorm.WithContext(ctx))
	type views struct {
		Author int64
		Total  int
	}
	rows, err := smorm.Scan[views](ctx, smorm.NewQuery[Post](em).
		Columns("{Author} AS author", "sum({Views}) AS total").GroupBy("{Author}"))
	if err != nil {
		panic(err)
	}
	fmt.Println(rows)
}
