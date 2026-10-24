package bench

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

type bunAuthor struct {
	bun.BaseModel `bun:"table:author,alias:a"`
	ID            int64 `bun:",pk,autoincrement"`
	Name          string
}

type bunPost struct {
	bun.BaseModel `bun:"table:post,alias:p"`
	ID            int64 `bun:",pk,autoincrement"`
	AuthorID      int64
	Author        *bunAuthor `bun:"rel:belongs-to,join:author_id=id"`
	Title         string
	Views         int32
}

func init() {
	var bdb *bun.DB
	setups = append(setups, func(db *sql.DB) error { bdb = bun.NewDB(db, pgdialect.New()); return nil })
	impls = append(impls, impl{
		name: "bun",
		insert: func(ctx context.Context, n int) error {
			posts := make([]bunPost, n)
			for i := range posts {
				posts[i] = bunPost{AuthorID: 1, Title: fmt.Sprint("new ", i)}
			}
			if n == 1 {
				_, err := bdb.NewInsert().Model(&posts[0]).Exec(ctx)
				return err
			}
			_, err := bdb.NewInsert().Model(&posts).Exec(ctx)
			return err
		},
		find: func(ctx context.Context, id int64) (string, error) {
			var p bunPost
			err := bdb.NewSelect().Model(&p).Where("p.id = ?", id).Scan(ctx)
			return p.Title, err
		},
		load100: func(ctx context.Context) ([]string, error) {
			var posts []bunPost
			err := bdb.NewSelect().Model(&posts).Relation("Author").Order("p.id").Limit(100).Scan(ctx)
			names := make([]string, len(posts))
			for i, p := range posts {
				names[i] = p.Author.Name
			}
			return names, err
		},
		update100: func(ctx context.Context) error {
			var posts []bunPost
			if err := bdb.NewSelect().Model(&posts).Order("p.id").Limit(100).Scan(ctx); err != nil {
				return err
			}
			for i := range posts {
				posts[i].Views++
			}
			_, err := bdb.NewUpdate().Model(&posts).Column("views").Bulk().Exec(ctx)
			return err
		},
	})
}
