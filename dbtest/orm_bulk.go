package dbtest

import (
	"context"
	"slices"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
)

func init() { Register(Case{"ORM/BulkUpdate", testORMBulkUpdate}) }

func testORMBulkUpdate(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	books := map[string]int{"ann": 2, "bob": 1, "cid": 0}
	authors := map[string]*ormAuthor{}
	for _, n := range []string{"ann", "bob", "cid"} {
		a := &ormAuthor{name: n}
		authors[n] = a
		must(t, em.Persist(a))
		for range books[n] {
			must(t, em.Persist(&ormBook{title: n, author: a}))
		}
	}
	must(t, em.Flush(ctx))

	var bulk [][]any
	em.Events().On(func(_ context.Context, a *smorm.EventArgs) error {
		bulk = append(bulk, a.IDs)
		return nil
	}, smorm.EventPostBulk)
	returns := db.Dialect().Features().Has(dialect.FeatureReturning)

	// An expression of the row itself (score is NULL for 0: nullzero).
	n, err := smorm.Update[ormAuthor](em).Set("score", smorm.Raw("COALESCE({score}, 0) + ?", 5)).Where(smorm.In("name", []string{"ann", "bob"})).Exec(ctx)
	must(t, err)
	if n != 2 || len(bulk) != 1 {
		t.Fatalf("updated %d rows, %d events", n, len(bulk))
	}
	if returns && !slices.ContainsFunc(bulk[0], func(id any) bool { return id == authors["ann"].ID() }) {
		t.Fatalf("event IDs %v", bulk[0])
	}
	scores := func() map[string]int {
		out := map[string]int{}
		all, err := smorm.NewQuery[ormAuthor](newEM()).All(ctx)
		must(t, err)
		for _, a := range all {
			out[a.name] = a.score
		}
		return out
	}
	if got := scores(); got["ann"] != 5 || got["bob"] != 5 || got["cid"] != 0 {
		t.Fatalf("scores %v", got)
	}
	if _, err := smorm.Update[ormAuthor](em).Set("score", 1).Exec(ctx); err == nil {
		t.Fatal("an update without condition must fail")
	}

	// Values from a sub query (UPDATE … FROM): the number of books.
	counts := smorm.NewQuery[ormAuthor](em).LeftJoinOn(ormBook{}, "b", smorm.Raw("{b.author} = {id}")).
		Columns("{id} AS id", "count({b.id}) AS n").GroupBy("{id}")
	upd := smorm.Update[ormAuthor](em).From(counts.Sub(), "v").Set("score", smorm.Raw("v.n")).Where(smorm.Raw("{id} = v.id"))
	if d := db.Dialect().Name(); d != dialect.Postgres && d != dialect.SQLite {
		if _, err := upd.Exec(ctx); err == nil {
			t.Fatal("UPDATE … FROM must fail on " + string(d))
		}
		return
	}
	n, err = upd.Exec(ctx)
	must(t, err)
	if n != 3 {
		t.Fatalf("updated %d rows from the sub query", n)
	}
	if got := scores(); got["ann"] != 2 || got["bob"] != 1 || got["cid"] != 0 {
		t.Fatalf("scores from the sub query %v", got)
	}

	// Inside a transaction the event waits for the commit, and a rollback
	// drops it.
	bulk = nil
	err = em.Transactional(ctx, func(ctx context.Context, em *smorm.EntityManager) error {
		if _, err := smorm.Update[ormAuthor](em).Set("score", 9).Where(smorm.Eq("name", "cid")).Exec(ctx); err != nil {
			return err
		}
		if len(bulk) != 0 {
			t.Error("event before the commit")
		}
		return nil
	})
	must(t, err)
	if len(bulk) != 1 {
		t.Fatalf("%d events after the commit", len(bulk))
	}
	bulk = nil
	_ = em.Transactional(ctx, func(ctx context.Context, em *smorm.EntityManager) error {
		if _, err := smorm.Update[ormAuthor](em).Set("score", 7).Where(smorm.Eq("name", "cid")).Exec(ctx); err != nil {
			return err
		}
		return context.Canceled
	})
	if len(bulk) != 0 || scores()["cid"] != 9 {
		t.Fatalf("rolled back: %d events, score %d", len(bulk), scores()["cid"])
	}
}

func init() { Register(Case{"ORM/BulkInsert", testORMBulkInsert}) }

func testORMBulkInsert(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	var events int
	em.Events().On(func(context.Context, *smorm.EventArgs) error { events++; return nil }, smorm.EventPostBulk)
	if d := db.Dialect().Name(); d != dialect.Postgres && d != dialect.SQLite {
		if _, err := smorm.Insert[ormAuthor](em).Rows(&ormAuthor{name: "ann"}).Exec(ctx); err == nil {
			t.Fatal("bulk inserts must fail on " + string(d))
		}
		return
	}
	n, err := smorm.Insert[ormAuthor](em).Rows(&ormAuthor{name: "ann", score: 1}, &ormAuthor{name: "bob", score: 2}).Exec(ctx)
	must(t, err)
	if n != 2 || events != 1 {
		t.Fatalf("inserted %d rows, %d events", n, events)
	}
	scores := func() map[string]int {
		out := map[string]int{}
		all, err := smorm.NewQuery[ormAuthor](newEM()).All(ctx)
		must(t, err)
		for _, a := range all {
			out[a.name] = a.score
			if a.CreatedAt().IsZero() {
				t.Errorf("%s without created_at", a.name)
			}
		}
		return out
	}
	// An upsert adds the proposed score to the existing one.
	n, err = smorm.Insert[ormAuthor](em).Rows(&ormAuthor{name: "ann", score: 10}, &ormAuthor{name: "cid", score: 3}).
		OnConflict("name").Set("score", smorm.Raw("COALESCE({score}, 0) + COALESCE({excluded.score}, 0)")).Exec(ctx)
	must(t, err)
	if got := scores(); n != 2 || got["ann"] != 11 || got["bob"] != 2 || got["cid"] != 3 {
		t.Fatalf("upsert of %d rows: %v", n, got)
	}
	n, err = smorm.Insert[ormAuthor](em).Rows(&ormAuthor{name: "bob", score: 9}).OnConflict("name").DoNothing().Exec(ctx)
	must(t, err)
	if got := scores(); n != 0 || got["bob"] != 2 {
		t.Fatalf("do nothing wrote %d rows: %v", n, got)
	}

	// INSERT … SELECT: a category per author, not twice (NOT EXISTS of the
	// sub query refers to the outer query by its alias).
	missing := func() *smorm.Query[ormAuthor] {
		has := smorm.NewQuery[ormCategory](em).Column("1").Where(smorm.Raw("{Name} = a.name"))
		return smorm.NewQuery[ormAuthor](em).As("a").Columns("{a.name}").Where(smorm.Raw("NOT EXISTS ?", has.Sub()))
	}
	for run, want := range []int{3, 0} {
		n, err = smorm.Insert[ormCategory](em).Select(missing(), "Name").Exec(ctx)
		must(t, err)
		if n != want {
			t.Fatalf("run %d inserted %d categories, want %d", run, n, want)
		}
	}
}
