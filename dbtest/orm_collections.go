package dbtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

func init() {
	Register(
		Case{"ORM/Collections", testORMCollections},
		Case{"ORM/GoStyleRef", testORMRef},
		Case{"ORM/JoinTargetOfAnotherManager", testJoinTargetOfAnotherManager},
	)
}

type colAuthor struct {
	smorm.Entity `smorm:"table:col_author"`
	ID           int64
	Name         string                    `smorm:"length:50"`
	Posts        smorm.Collection[colPost] `smorm:"oneToMany,mappedBy:Author,cascade:persist|remove,orphanRemoval,orderBy:Title,fetch:extraLazy"`
	Tags         smorm.Collection[colTag]  `smorm:"manyToMany,inversedBy:Authors,cascade:persist,joinTable:col_author_tag"`
}

// colReader links tags without cascading: they are stored on their own.
type colReader struct {
	smorm.Entity `smorm:"table:col_reader"`
	ID           int64
	Name         string                   `smorm:"length:50"`
	Tags         smorm.Collection[colTag] `smorm:"manyToMany,joinTable:col_reader_tag"`
}

type colPost struct {
	smorm.Entity `smorm:"table:col_post"`
	ID           int64
	Title        string     `smorm:"length:50"`
	Author       *colAuthor `smorm:"inversedBy:Posts,notnull,onDelete:cascade"`
}

type colTag struct {
	smorm.Entity `smorm:"table:col_tag"`
	ID           int64
	Label        string                      `smorm:"length:50"`
	Authors      smorm.Collection[colAuthor] `smorm:"manyToMany,mappedBy:Tags"`
}

type colComment struct {
	smorm.Entity `smorm:"table:col_comment"`
	ID           int64
	Body         string             `smorm:"length:50"`
	Post         smorm.Ref[colPost] `smorm:"manyToOne"`
}

func titles(posts []*colPost) []string {
	var out []string
	for _, p := range posts {
		out = append(out, p.Title)
	}
	return out
}

func labels(tags []*colTag) []string {
	var out []string
	for _, t := range tags {
		out = append(out, t.Label)
	}
	slices.Sort(out)
	return out
}

func testORMCollections(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, colAuthor{}, colComment{})
	em := newEM()

	a := &colAuthor{Name: "ann"}
	for _, title := range []string{"zeta", "alpha"} {
		a.Posts.Add(&colPost{Title: title, Author: a})
	}
	a.Tags.Add(&colTag{Label: "go"}, &colTag{Label: "sql"})
	must(t, em.Persist(a)) // cascades to posts and tags
	must(t, em.Flush(ctx))
	if n := Count(t, db, "col_post"); n != 2 {
		t.Fatalf("posts = %d", n)
	}
	if n := Count(t, db, "col_author_tag"); n != 2 {
		t.Fatalf("join rows = %d", n)
	}

	var postSelects int
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "SELECT" && strings.Contains(ev.Query, "col_post") && !strings.Contains(ev.Query, "COUNT") {
			postSelects++
		}
	}})
	em2 := newEM()
	la, err := smorm.Find[colAuthor](ctx, em2, a.ID)
	must(t, err)
	if la.Posts.IsInitialized() || la.Posts.Len() != 2 || la.Posts.IsInitialized() || postSelects != 0 {
		t.Fatal("extra lazy Len must count without loading")
	}
	if got := titles(la.Posts.All()); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Fatalf("posts = %v", got)
	}
	if postSelects != 1 || la.Posts.Get(0).Author != la {
		t.Fatal("posts must load once and point back to the owner")
	}
	if got := labels(la.Tags.All()); !slices.Equal(got, []string{"go", "sql"}) {
		t.Fatalf("tags = %v", got)
	}
	// Inverse side of manyToMany loads too.
	goTag := la.Tags.All()[slices.IndexFunc(la.Tags.All(), func(x *colTag) bool { return x.Label == "go" })]
	if authors := goTag.Authors.All(); len(authors) != 1 || authors[0] != la {
		t.Fatalf("tag authors = %v", authors)
	}

	// Orphan removal, join table removal and addition.
	zeta := la.Posts.All()[1]
	if !la.Posts.Remove(zeta) || la.Posts.Remove(zeta) || la.Posts.Contains(zeta) {
		t.Fatal("Remove")
	}
	la.Tags.Remove(goTag)
	la.Tags.Add(&colTag{Label: "orm"})
	must(t, em2.Flush(ctx))
	if n := Count(t, db, "col_post"); n != 1 {
		t.Fatalf("orphan not removed: %d posts", n)
	}
	if n := Count(t, db, "col_tag"); n != 3 {
		t.Fatalf("tags = %d (removing from a manyToMany must not delete the tag)", n)
	}
	em3 := newEM()
	la3, err := smorm.Find[colAuthor](ctx, em3, a.ID)
	must(t, err)
	if got := labels(la3.Tags.All()); !slices.Equal(got, []string{"orm", "sql"}) {
		t.Fatalf("tags after change = %v", got)
	}
	// Extra lazy Add does not load; Len includes pending items.
	la3.Posts.Add(&colPost{Title: "beta", Author: la3})
	if la3.Posts.IsInitialized() || la3.Posts.Len() != 2 {
		t.Fatal("extra lazy Add")
	}
	must(t, em3.Flush(ctx))
	if n := Count(t, db, "col_post"); n != 2 {
		t.Fatalf("posts = %d", n)
	}
	// Explicit loading and detached collections.
	em4 := newEM()
	la4, err := smorm.Find[colAuthor](ctx, em4, a.ID)
	must(t, err)
	must(t, smorm.Load(ctx, &la4.Tags))
	if !la4.Tags.IsInitialized() {
		t.Fatal("Load collection")
	}
	em4.Clear()
	if err := smorm.CatchLazy(func() error { la4.Posts.All(); return nil }); !errors.Is(err, smorm.ErrDetached) {
		t.Fatalf("detached collection = %v", err)
	}
	// Removing the owner cascades to its posts.
	em5 := newEM()
	la5, err := smorm.Find[colAuthor](ctx, em5, a.ID)
	must(t, err)
	must(t, em5.Remove(la5))
	must(t, em5.Flush(ctx))
	if Count(t, db, "col_post") != 0 || Count(t, db, "col_author_tag") != 0 {
		t.Fatal("cascade remove")
	}
}

func testORMRef(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, colAuthor{}, colComment{})
	em := newEM()
	a := &colAuthor{Name: "a"}
	p := &colPost{Title: "p", Author: a}
	c := &colComment{Body: "hi", Post: smorm.NewRef(p)}
	empty := &colComment{Body: "none"}
	for _, x := range []any{a, p, c, empty} {
		must(t, em.Persist(x))
	}
	must(t, em.Flush(ctx))
	em2 := newEM()
	lc, err := smorm.Find[colComment](ctx, em2, c.ID)
	must(t, err)
	if lc.Post.IsNil() || em2.IsInitialized(lc.Post.Ptr()) {
		t.Fatal("ref must be an uninitialized stub")
	}
	if lc.Post.Get().Title != "p" || !em2.IsInitialized(lc.Post.Ptr()) {
		t.Fatal("Get must load")
	}
	le, err := smorm.Find[colComment](ctx, em2, empty.ID)
	must(t, err)
	if !le.Post.IsNil() || le.Post.Get() != nil {
		t.Fatal("empty ref")
	}
	other, _ := smorm.Find[colPost](ctx, em2, p.ID)
	le.Post.Set(other)
	must(t, em2.Flush(ctx))
	check, err := smorm.Find[colComment](ctx, newEM(), empty.ID)
	must(t, err)
	if check.Post.Get().ID != p.ID {
		t.Fatal("Ref.Set must write the FK")
	}
	must(t, smorm.Load(ctx, &check.Post))
}

// testJoinTargetOfAnotherManager: a stored record that another entity
// manager loaded is linked by its ID (it used to be left out without a
// word); a record never stored cannot be linked.
func testJoinTargetOfAnotherManager(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, colReader{}, colAuthor{})
	tag := &colTag{Label: "stored"}
	first := newEM()
	must(t, first.Persist(tag))
	must(t, first.Flush(ctx))

	second := newEM()
	r := &colReader{Name: "ben"}
	r.Tags.Add(tag) // tracked by the first manager only
	must(t, second.Persist(r))
	must(t, second.Flush(ctx))
	if n := Count(t, db, "col_reader_tag"); n != 1 {
		t.Fatalf("join rows = %d, want the link to the stored tag", n)
	}

	third := newEM()
	n := &colReader{Name: "cid"}
	n.Tags.Add(&colTag{Label: "never stored"})
	must(t, third.Persist(n))
	var nf *smorm.NewEntityFoundError
	if err := third.Flush(ctx); !errors.As(err, &nf) {
		t.Fatalf("a tag never stored: %v", err)
	}
}
