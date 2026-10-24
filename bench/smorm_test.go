package bench

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect/pgdialect"

	"github.com/smatify/smorm/bench/model"
)

func init() {
	var sdb *dbal.DB
	setups = append(setups, func(db *sql.DB) error { sdb = dbal.NewDB(db, pgdialect.New()); return nil })
	newEM := func(ctx context.Context) *smorm.EntityManager {
		return smorm.NewEntityManager(sdb, smorm.WithContext(ctx))
	}
	first100 := func(ctx context.Context, em *smorm.EntityManager, with ...any) ([]*model.Post, error) {
		return smorm.NewQuery[model.Post](em).With(with...).OrderBy(model.PostFields.ID, smorm.Asc).Limit(100).All(ctx)
	}
	impls = append(impls, impl{
		name: "smorm",
		insert: func(ctx context.Context, n int) error {
			em := newEM(ctx)
			author, err := smorm.Reference[model.Author](em, int64(1))
			if err != nil {
				return err
			}
			for i := range n {
				if err := em.Persist((&model.Post{}).SetAuthor(author).SetTitle(fmt.Sprint("new ", i))); err != nil {
					return err
				}
			}
			return em.Flush(ctx)
		},
		find: func(ctx context.Context, id int64) (string, error) {
			p, err := smorm.Find[model.Post](ctx, newEM(ctx), id)
			if err != nil {
				return "", err
			}
			return p.Title(), nil
		},
		load100: func(ctx context.Context) ([]string, error) {
			posts, err := first100(ctx, newEM(ctx), model.PostFields.Author)
			names := make([]string, len(posts))
			for i, p := range posts {
				names[i] = p.Author().Name()
			}
			return names, err
		},
		update100: func(ctx context.Context) error {
			em := newEM(ctx)
			posts, err := first100(ctx, em)
			if err != nil {
				return err
			}
			for _, p := range posts {
				p.SetViews(p.Views() + 1)
			}
			return em.Flush(ctx)
		},
	})
}
