package bench

import (
	"context"
	"database/sql"
	"fmt"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/smatify/tenet/bench/ent"
	"github.com/smatify/tenet/bench/ent/post"
)

func init() {
	var client *ent.Client
	setups = append(setups, func(db *sql.DB) error {
		client = ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
		return nil
	})
	first100 := func(q *ent.PostQuery) *ent.PostQuery { return q.Order(ent.Asc(post.FieldID)).Limit(100) }
	impls = append(impls, impl{
		name: "ent",
		insert: func(ctx context.Context, n int) error {
			if n == 1 {
				return client.Post.Create().SetAuthorID(1).SetTitle("new 0").Exec(ctx)
			}
			builders := make([]*ent.PostCreate, n)
			for i := range builders {
				builders[i] = client.Post.Create().SetAuthorID(1).SetTitle(fmt.Sprint("new ", i))
			}
			return client.Post.CreateBulk(builders...).Exec(ctx)
		},
		find: func(ctx context.Context, id int64) (string, error) {
			p, err := client.Post.Get(ctx, id)
			if err != nil {
				return "", err
			}
			return p.Title, nil
		},
		load100: func(ctx context.Context) ([]string, error) {
			posts, err := first100(client.Post.Query().WithAuthor()).All(ctx)
			names := make([]string, len(posts))
			for i, p := range posts {
				names[i] = p.Edges.Author.Name
			}
			return names, err
		},
		update100: func(ctx context.Context) error {
			posts, err := first100(client.Post.Query()).All(ctx)
			if err != nil {
				return err
			}
			tx, err := client.Tx(ctx)
			if err != nil {
				return err
			}
			for _, p := range posts {
				if err := tx.Post.UpdateOneID(p.ID).SetViews(p.Views + 1).Exec(ctx); err != nil {
					_ = tx.Rollback()
					return err
				}
			}
			return tx.Commit()
		},
	})
}
