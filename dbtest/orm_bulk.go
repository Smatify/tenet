package dbtest

import (
	"context"
	"slices"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect"
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
	em.Events().On(func(_ context.Context, a *tenet.EventArgs) error {
		bulk = append(bulk, a.IDs)
		return nil
	}, tenet.EventPostBulk)
	returns := db.Dialect().Features().Has(dialect.FeatureReturning)

	// An expression of the row itself (score is NULL for 0: nullzero).
	n, err := tenet.Update[ormAuthor](em).Set("score", tenet.Raw("COALESCE({score}, 0) + ?", 5)).Where(tenet.In("name", []string{"ann", "bob"})).Exec(ctx)
	must(t, err)
	if n != 2 || len(bulk) != 1 {
		t.Fatalf("updated %d rows, %d events", n, len(bulk))
	}
	if returns && !slices.ContainsFunc(bulk[0], func(id any) bool { return id == authors["ann"].ID() }) {
		t.Fatalf("event IDs %v", bulk[0])
	}
	scores := func() map[string]int {
		out := map[string]int{}
		all, err := tenet.NewQuery[ormAuthor](newEM()).All(ctx)
		must(t, err)
		for _, a := range all {
			out[a.name] = a.score
		}
		return out
	}
	if got := scores(); got["ann"] != 5 || got["bob"] != 5 || got["cid"] != 0 {
		t.Fatalf("scores %v", got)
	}
	if n, err := tenet.Update[ormAuthor](em).Set("score", 1).Where(tenet.Eq("name", "nobody")).Exec(ctx); err != nil || n != 0 || (returns && len(bulk) != 1) {
		t.Fatalf("an update of no rows: %d rows, %d events (%v)", n, len(bulk), err)
	}
	if _, err := tenet.Update[ormAuthor](em).Set("score", 1).Exec(ctx); err == nil {
		t.Fatal("an update without condition must fail")
	}

	// Values from a sub query (UPDATE … FROM): the number of books.
	counts := tenet.NewQuery[ormAuthor](em).LeftJoinOn(ormBook{}, "b", tenet.Raw("{b.author} = {id}")).
		Columns("{id} AS id", "count({b.id}) AS n").GroupBy("{id}")
	upd := tenet.Update[ormAuthor](em).From(counts.Sub(), "v").Set("score", tenet.Raw("v.n")).Where(tenet.Raw("{id} = v.id"))
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
	err = em.Transactional(ctx, func(ctx context.Context, em *tenet.EntityManager) error {
		if _, err := tenet.Update[ormAuthor](em).Set("score", 9).Where(tenet.Eq("name", "cid")).Exec(ctx); err != nil {
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
	_ = em.Transactional(ctx, func(ctx context.Context, em *tenet.EntityManager) error {
		if _, err := tenet.Update[ormAuthor](em).Set("score", 7).Where(tenet.Eq("name", "cid")).Exec(ctx); err != nil {
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
	em.Events().On(func(context.Context, *tenet.EventArgs) error { events++; return nil }, tenet.EventPostBulk)
	if d := db.Dialect().Name(); d != dialect.Postgres && d != dialect.SQLite {
		if _, err := tenet.Insert[ormAuthor](em).Rows(&ormAuthor{name: "ann"}).Exec(ctx); err == nil {
			t.Fatal("bulk inserts must fail on " + string(d))
		}
		return
	}
	n, err := tenet.Insert[ormAuthor](em).Rows(&ormAuthor{name: "ann", score: 1}, &ormAuthor{name: "bob", score: 2}).Exec(ctx)
	must(t, err)
	if n != 2 || events != 1 {
		t.Fatalf("inserted %d rows, %d events", n, events)
	}
	scores := func() map[string]int {
		out := map[string]int{}
		all, err := tenet.NewQuery[ormAuthor](newEM()).All(ctx)
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
	n, err = tenet.Insert[ormAuthor](em).Rows(&ormAuthor{name: "ann", score: 10}, &ormAuthor{name: "cid", score: 3}).
		OnConflict("name").Set("score", tenet.Raw("COALESCE({score}, 0) + COALESCE({excluded.score}, 0)")).Exec(ctx)
	must(t, err)
	if got := scores(); n != 2 || got["ann"] != 11 || got["bob"] != 2 || got["cid"] != 3 {
		t.Fatalf("upsert of %d rows: %v", n, got)
	}
	n, err = tenet.Insert[ormAuthor](em).Rows(&ormAuthor{name: "bob", score: 9}).OnConflict("name").DoNothing().Exec(ctx)
	must(t, err)
	if got := scores(); n != 0 || got["bob"] != 2 {
		t.Fatalf("do nothing wrote %d rows: %v", n, got)
	}

	// INSERT … SELECT: a category per author, not twice (NOT EXISTS of the
	// sub query refers to the outer query by its alias).
	missing := func() *tenet.Query[ormAuthor] {
		has := tenet.NewQuery[ormCategory](em).Column("1").Where(tenet.Raw("{Name} = a.name"))
		return tenet.NewQuery[ormAuthor](em).As("a").Columns("{a.name}").Where(tenet.Raw("NOT EXISTS ?", has.Sub()))
	}
	for run, want := range []int{3, 0} {
		n, err = tenet.Insert[ormCategory](em).Select(missing(), "Name").Exec(ctx)
		must(t, err)
		if n != want {
			t.Fatalf("run %d inserted %d categories, want %d", run, n, want)
		}
	}
}

func init() { Register(Case{"ORM/BulkDelete", testORMBulkDelete}) }

func testORMBulkDelete(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	for _, n := range []string{"ann", "bob", "cid"} {
		must(t, em.Persist(&ormAuthor{name: n}))
	}
	must(t, em.Flush(ctx))
	var ids [][]any
	em.Events().On(func(_ context.Context, a *tenet.EventArgs) error { ids = append(ids, a.IDs); return nil }, tenet.EventPostBulk)
	if _, err := tenet.Delete[ormAuthor](em).Exec(ctx); err == nil {
		t.Fatal("a delete without condition must fail")
	}
	n, err := tenet.Delete[ormAuthor](em).Where(tenet.In("name", []string{"ann", "bob"})).Exec(ctx)
	must(t, err)
	if n != 2 || len(ids) != 1 {
		t.Fatalf("deleted %d rows, %d events", n, len(ids))
	}
	if db.Dialect().Features().Has(dialect.FeatureReturning) && len(ids[0]) != 2 {
		t.Fatalf("event IDs %v", ids[0])
	}
	if n, err := tenet.Delete[ormAuthor](em).Where(tenet.Eq("name", "nobody")).Exec(ctx); err != nil || n != 0 || len(ids) != 1 {
		t.Fatalf("deleting nothing: %d rows, %d events (%v)", n, len(ids), err)
	}
	left, err := tenet.NewQuery[ormAuthor](newEM()).All(ctx)
	must(t, err)
	if len(left) != 1 || left[0].name != "cid" {
		t.Fatalf("left %v", left)
	}
}
