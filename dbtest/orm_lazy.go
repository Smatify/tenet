package dbtest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
)

func init() { Register(Case{"ORM/LazyLoadingAndBatching", testORMLazy}) }

// Name is a Doctrine style getter: it loads the author on first access.
func (a *ormAuthor) Name() string { tenet.Touch(a); return a.name }

func testORMLazy(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	var books []*ormBook
	for _, n := range []string{"a", "b", "c"} {
		a := &ormAuthor{name: n}
		b := &ormBook{title: "book " + n, author: a}
		must(t, em.Persist(a))
		must(t, em.Persist(b))
		books = append(books, b)
	}
	must(t, em.Flush(ctx))

	var authorSelects int
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "SELECT" && strings.Contains(ev.Query, "orm_author") {
			authorSelects++
		}
	}})
	em2 := tenet.NewEntityManager(db, tenet.WithRegistry(em.Registry()), tenet.WithContext(ctx))
	var loaded []*ormBook
	for _, b := range books {
		lb, err := tenet.Find[ormBook](ctx, em2, b.ID())
		must(t, err)
		loaded = append(loaded, lb)
	}
	if authorSelects != 0 || em2.IsInitialized(loaded[0].author) {
		t.Fatal("authors must not be loaded yet")
	}
	if got := loaded[1].author.Name(); got != "b" {
		t.Fatalf("Name() = %q", got)
	}
	// One batched query initialized every author reference.
	if authorSelects != 1 || loaded[0].author.Name() != "a" || loaded[2].author.Name() != "c" || authorSelects != 1 {
		t.Fatalf("expected one batched query, got %d", authorSelects)
	}

	// Explicit loading returns errors instead of panicking.
	em3 := newEM()
	ref, err := tenet.Reference[ormAuthor](em3, int64(987654))
	must(t, err)
	if err := tenet.Load(ctx, ref); !errors.Is(err, tenet.ErrNotFound) {
		t.Fatalf("Load missing = %v", err)
	}
	err = tenet.CatchLazy(func() error { _ = ref.Name(); return nil })
	var le *tenet.LazyLoadError
	if !errors.As(err, &le) || !errors.Is(err, tenet.ErrNotFound) {
		t.Fatalf("CatchLazy = %v", err)
	}

	// Detached references cannot load.
	em4 := newEM()
	lb, err := tenet.Find[ormBook](ctx, em4, books[0].ID())
	must(t, err)
	em4.Clear()
	err = tenet.CatchLazy(func() error { _ = lb.author.Name(); return nil })
	if !errors.Is(err, tenet.ErrDetached) {
		t.Fatalf("detached = %v", err)
	}
	// The middleware turns the panic into a response.
	h := tenet.RecoverLazyLoad(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { _ = lb.author.Name() }), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}
}
