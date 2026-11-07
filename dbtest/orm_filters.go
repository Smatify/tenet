package dbtest

import (
	"context"
	"errors"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
)

func init() { Register(Case{"ORM/FiltersAndSoftDelete", testORMFilters}) }

type sdPost struct {
	tenet.Entity `tenet:"table:sd_post"`
	tenet.IntID
	tenet.SoftDelete
	Title    string                      `tenet:"length:50"`
	TenantID int64                       `tenet:"column:tenant_id"`
	Comments tenet.Collection[sdComment] `tenet:"oneToMany,mappedBy:Post,fetch:extraLazy"`
}

type sdComment struct {
	tenet.Entity `tenet:"table:sd_comment"`
	tenet.IntID
	tenet.SoftDelete
	Body string  `tenet:"length:50"`
	Post *sdPost `tenet:"inversedBy:Comments,notnull"`
}

func testORMFilters(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, sdPost{})
	em := newEM()
	p1, p2, p3 := &sdPost{Title: "a", TenantID: 1}, &sdPost{Title: "b", TenantID: 1}, &sdPost{Title: "c", TenantID: 2}
	c1, c2 := &sdComment{Body: "x", Post: p1}, &sdComment{Body: "y", Post: p2}
	c3 := &sdComment{Body: "z", Post: p1}
	for _, v := range []any{p1, p2, p3, c1, c2, c3} {
		must(t, em.Persist(v))
	}
	must(t, em.Flush(ctx))

	// Soft delete: the row stays, the entity disappears.
	must(t, em.Remove(p2))
	must(t, em.Remove(c3))
	must(t, em.Flush(ctx))
	if Count(t, db, "sd_post") != 3 || !p2.IsDeleted() || em.Contains(p2) {
		t.Fatal("soft delete must keep the row and detach the entity")
	}
	em2 := newEM()
	if _, err := tenet.Find[sdPost](ctx, em2, p2.ID()); !errors.Is(err, tenet.ErrNotFound) {
		t.Fatalf("Find deleted = %v", err)
	}
	repo := tenet.RepositoryFor[sdPost](em2)
	if all, _ := repo.FindAll(ctx); len(all) != 2 {
		t.Fatalf("FindAll = %d", len(all))
	}
	if n, _ := repo.Count(ctx, nil); n != 2 {
		t.Fatalf("Count = %d", n)
	}
	lp1, err := repo.Find(ctx, p1.ID())
	must(t, err)
	if n := lp1.Comments.Len(); n != 1 {
		t.Fatalf("extra lazy count must skip deleted comments, got %d", n)
	}
	if all := lp1.Comments.All(); len(all) != 1 || all[0].Body != "x" {
		t.Fatalf("collection = %v", all)
	}
	// Joins honour filters of the joined entity.
	comments, err := tenet.NewQuery[sdComment](em2).Join("Post", "p").All(ctx)
	must(t, err)
	if len(comments) != 1 {
		t.Fatalf("join filter: %d comments", len(comments))
	}
	// Tenant filter.
	em3 := newEM()
	em3.Filters().Enable("tenant", tenet.ColumnFilter("tenant_id", 1))
	tenant, err := tenet.RepositoryFor[sdPost](em3).FindAll(ctx)
	must(t, err)
	if len(tenant) != 1 || tenant[0].Title != "a" {
		t.Fatalf("tenant filter = %v", tenant)
	}
	if !em3.Filters().IsEnabled("tenant") || len(em3.Filters().Enabled()) != 2 {
		t.Fatal("Enabled")
	}
	// A reference to a soft-deleted entity still resolves (the row exists):
	// lazily and preloaded.
	lazy, err := tenet.Find[sdComment](ctx, newEM(), c2.ID())
	must(t, err)
	tenet.Touch(lazy.Post)
	if lazy.Post.Title != "b" {
		t.Fatalf("lazy reference to a deleted post = %q", lazy.Post.Title)
	}
	preloaded, err := tenet.NewQuery[sdComment](newEM()).Where(tenet.Eq("ID", c2.ID())).With("Post").All(ctx)
	must(t, err)
	if len(preloaded) != 1 || preloaded[0].Post.Title != "b" {
		t.Fatalf("preloaded reference to a deleted post = %v", preloaded)
	}
	// Restore and hard delete with the soft delete filter disabled.
	em4 := newEM()
	em4.Filters().Disable(tenet.SoftDeleteFilterName)
	deleted, err := tenet.Find[sdPost](ctx, em4, p2.ID())
	must(t, err)
	if !deleted.IsDeleted() || deleted.DeletedAt() == nil {
		t.Fatal("deleted_at must be loaded")
	}
	deleted.Restore()
	must(t, em4.Flush(ctx))
	if _, err := tenet.Find[sdPost](ctx, newEM(), p2.ID()); err != nil {
		t.Fatalf("restored post: %v", err)
	}
	em5 := newEM()
	em5.Filters().Disable(tenet.SoftDeleteFilterName)
	gone, err := tenet.Find[sdComment](ctx, em5, c3.ID())
	must(t, err)
	must(t, em5.Remove(gone)) // already soft-deleted: hard delete
	must(t, em5.Flush(ctx))
	if Count(t, db, "sd_comment") != 2 {
		t.Fatal("second remove must delete the row")
	}
}
