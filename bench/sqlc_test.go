package bench

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/smatify/tenet/bench/sqlcdb"
)

func init() {
	var sdb *sql.DB
	var q *sqlcdb.Queries
	setups = append(setups, func(db *sql.DB) error { sdb, q = db, sqlcdb.New(db); return nil })
	// inTx runs fn with queries bound to a transaction.
	inTx := func(ctx context.Context, fn func(q *sqlcdb.Queries) error) error {
		tx, err := sdb.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := fn(q.WithTx(tx)); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	impls = append(impls, impl{
		name: "sqlc",
		insert: func(ctx context.Context, n int) error {
			if n == 1 {
				_, err := q.InsertPost(ctx, sqlcdb.InsertPostParams{AuthorID: 1, Title: "new 0"})
				return err
			}
			return inTx(ctx, func(q *sqlcdb.Queries) error {
				for i := range n {
					if _, err := q.InsertPost(ctx, sqlcdb.InsertPostParams{AuthorID: 1, Title: fmt.Sprint("new ", i)}); err != nil {
						return err
					}
				}
				return nil
			})
		},
		find: func(ctx context.Context, id int64) (string, error) {
			p, err := q.GetPost(ctx, id)
			return p.Title, err
		},
		load100: func(ctx context.Context) ([]string, error) {
			rows, err := q.ListPostsWithAuthor(ctx, 100)
			names := make([]string, len(rows))
			for i, r := range rows {
				names[i] = r.Author.Name
			}
			return names, err
		},
		update100: func(ctx context.Context) error {
			posts, err := q.ListPosts(ctx, 100)
			if err != nil {
				return err
			}
			return inTx(ctx, func(q *sqlcdb.Queries) error {
				for _, p := range posts {
					if err := q.UpdatePostViews(ctx, sqlcdb.UpdatePostViewsParams{ID: p.ID, Views: p.Views + 1}); err != nil {
						return err
					}
				}
				return nil
			})
		},
	})
}
