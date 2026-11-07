package dbtest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
)

func init() { Register(Case{"ORM/PreloadAndEager", testORMPreload}) }

type eOwner struct {
	tenet.Entity `tenet:"table:e_owner"`
	ID           int64
	Name         string  `tenet:"length:50"`
	Pets         []*ePet `tenet:"oneToMany,mappedBy:Owner,orderBy:Name"`
}

type ePet struct {
	tenet.Entity `tenet:"table:e_pet"`
	ID           int64
	Name         string  `tenet:"length:50"`
	Owner        *eOwner `tenet:"inversedBy:Pets,fetch:eager"`
}

func testORMPreload(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, colAuthor{}, eOwner{})
	em := newEM()
	for i, n := range []string{"a", "b", "c"} {
		a := &colAuthor{Name: n}
		for j := range i + 1 {
			a.Posts.Add(&colPost{Title: n + string(rune('0'+j)), Author: a})
		}
		a.Tags.Add(&colTag{Label: "t" + n}, &colTag{Label: "shared" + n})
		must(t, em.Persist(a))
	}
	must(t, em.Flush(ctx))
	o := &eOwner{Name: "olga"}
	must(t, em.Persist(o))
	for _, n := range []string{"rex", "bello"} {
		must(t, em.Persist(&ePet{Name: n, Owner: o}))
	}
	must(t, em.Flush(ctx))

	queries := 0
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "SELECT" && strings.Contains(ev.Query, "col_") {
			queries++
		}
	}})
	em2 := newEM()
	var roots []any
	rows, err := db.NewSelect().Column("id").Table("col_author").OrderBy("id", dbal.Asc).ScanMaps(ctx)
	must(t, err)
	for _, r := range rows {
		a, err := tenet.Find[colAuthor](ctx, em2, r["id"])
		must(t, err)
		roots = append(roots, a)
	}
	queries = 0
	must(t, em2.Preload(ctx, roots, "Posts", "Tags", "Posts.Author"))
	if queries != 3 {
		t.Fatalf("preload issued %d queries, want 3 (posts, join pairs, tags)", queries)
	}
	for i, r := range roots {
		a := r.(*colAuthor)
		if !a.Posts.IsInitialized() || !a.Tags.IsInitialized() || len(a.Posts.All()) != i+1 || len(a.Tags.All()) != 2 {
			t.Fatalf("author %d: posts=%d tags=%d", i, len(a.Posts.All()), len(a.Tags.All()))
		}
		for _, p := range a.Posts.All() {
			if p.Author != a {
				t.Fatal("back reference")
			}
		}
	}
	if queries != 3 {
		t.Fatal("using preloaded collections must not query")
	}
	if err := em2.Preload(ctx, roots, "Nope"); err == nil {
		t.Fatal("unknown path")
	}
	// Slices are loaded eagerly; fetch:eager to-one associations too.
	em3 := newEM()
	lo, err := tenet.Find[eOwner](ctx, em3, o.ID)
	must(t, err)
	var names []string
	for _, p := range lo.Pets {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"bello", "rex"}) {
		t.Fatalf("pets = %v", names)
	}
	em4 := newEM()
	pet, err := tenet.Find[ePet](ctx, em4, lo.Pets[0].ID)
	must(t, err)
	if !em4.IsInitialized(pet.Owner) || pet.Owner.Name != "olga" || len(pet.Owner.Pets) != 2 {
		t.Fatal("eager manyToOne must load the owner (and its eager pets)")
	}
}
