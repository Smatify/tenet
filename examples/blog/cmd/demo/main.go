package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect/sqlitedialect"

	"example.com/blog/internal/entity"
)

func main() {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", os.Getenv("TENET_DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	db := dbal.NewDB(sqlDB, sqlitedialect.New())
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))

	ann := (&entity.Author{}).SetName("Ann")
	ann.AddPost((&entity.Post{}).SetTitle("Hello"))
	ann.AddPost((&entity.Post{}).SetTitle("World"))
	if err := em.Persist(ann); err != nil {
		panic(err)
	}
	for _, p := range ann.Posts() {
		if err := em.Persist(p); err != nil {
			panic(err)
		}
	}
	if err := em.Flush(ctx); err != nil {
		panic(err)
	}

	em2 := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	posts, err := entity.NewPostRepository(em2).FindBy(ctx, tenet.Criteria{"title": "World"})
	if err != nil {
		panic(err)
	}
	p := posts[0]
	fmt.Printf("post=%s author=%s initialized=%v\n", p.Title(), p.Author().Name(), em2.IsInitialized(p.Author()))
	fmt.Printf("author posts=%d\n", len(p.Author().Posts()))
}
