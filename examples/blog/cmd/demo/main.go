// Command demo stores a few posts and reads them back the ways an
// application usually does: a query with a join, lazy loading, a
// projection and a bulk update.
//
//	export TENET_DATABASE_URL="file:app.db?_pragma=foreign_keys(1)"
//	go run ./cmd/console migrations:migrate -n
//	go run ./cmd/demo
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect/sqlitedialect"

	"example.com/blog/internal/entity"
)

func main() {
	ctx := context.Background()
	db, err := dbal.Open("sqlite", os.Getenv("TENET_DATABASE_URL"), sqlitedialect.New())
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if err := run(ctx, db, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, db *dbal.DB, out io.Writer) error {
	// One entity manager per request or job.
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	ann := (&entity.Author{}).SetName("Ann")
	ann.AddPost((&entity.Post{}).SetTitle("Hello"))
	ann.AddPost((&entity.Post{}).SetTitle("World"))
	bob := (&entity.Author{}).SetName("Bob")
	bob.AddPost((&entity.Post{}).SetTitle("Hi"))
	for _, a := range []*entity.Author{ann, bob} {
		if err := em.Persist(a); err != nil {
			return err
		}
		for _, p := range a.Posts() { // Author.posts does not cascade persist
			if err := em.Persist(p); err != nil {
				return err
			}
		}
	}
	if err := em.Flush(ctx); err != nil { // one transaction, authors first
		return err
	}

	// A fresh entity manager: nothing is loaded yet.
	em = tenet.NewEntityManager(db, tenet.WithContext(ctx))
	posts, err := tenet.NewQuery[entity.Post](em).
		Join(entity.PostFields.Author, "a").
		Where(tenet.Like(entity.AuthorFields.Name, "A%")).
		OrderBy(entity.PostFields.Title, tenet.Asc).
		With(entity.PostFields.Author). // joined into the query
		All(ctx)
	if err != nil {
		return err
	}
	for _, p := range posts {
		fmt.Fprintf(out, "%s by %s\n", p.Title(), p.Author().Name())
	}

	post, err := entity.NewPostRepository(em).FindOneBy(ctx, tenet.Criteria{"title": "Hi"})
	if err != nil {
		return err
	}
	// The getters load the author and its posts when they are used.
	fmt.Fprintf(out, "%s wrote %d post(s)\n", post.Author().Name(), len(post.Author().Posts()))

	type count struct {
		Name  string
		Posts int
	}
	counts, err := tenet.Scan[count](ctx, tenet.NewQuery[entity.Author](em).As("a").
		LeftJoin(entity.AuthorFields.Posts, "p").
		Columns("{a.name} AS name", "count({p.id}) AS posts").
		GroupBy("{a.name}").
		OrderBy(entity.AuthorFields.Name, tenet.Asc))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%+v\n", counts)

	// Many rows in one statement, without loading them.
	n, err := tenet.Update[entity.Post](em).Set("subtitle", "draft").
		Where(tenet.IsNull(entity.PostFields.Subtitle)).Exec(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%d posts marked as draft\n", n)
	return nil
}
