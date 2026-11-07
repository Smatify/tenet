package orm

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/smatify/tenet/dialect"
	"github.com/smatify/tenet/internal/entity"
)

func panicErr(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err, _ = r.(error)
		}
	}()
	fn()
	return nil
}

func TestEnsureLoaded(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.Postgres)
	em.SetContext(ctx)
	if em.Context() != ctx {
		t.Fatal("context")
	}
	rec.Returns("FROM", []string{"id", "name"}, []driver.Value{int64(1), "one"}, []driver.Value{int64(2), "two"})
	r1, _ := em.Reference(typeOf[uAuthor](), 1)
	r2, _ := em.Reference(typeOf[uAuthor](), int32(2))
	entity.Touch(r1)
	if r1.(*uAuthor).Name != "one" || r2.(*uAuthor).Name != "two" {
		t.Fatal("batch must initialize both references")
	}
	q := rec.Queries()
	if len(q) != 1 || !strings.Contains(q[0], `"id" IN ($1, $2)`) && !strings.Contains(q[0], `"id" IN ($2, $1)`) {
		t.Fatalf("queries = %v", q)
	}
	entity.Touch(r1) // already loaded: no query
	if len(rec.Queries()) != 1 {
		t.Fatal("second touch queried")
	}

	// Batch size 1 refreshes one at a time.
	em1, rec1 := newUnitEM(t, dialect.Postgres)
	WithBatchSize(0)(em1)
	rec1.Returns("FROM", []string{"id", "name"}, []driver.Value{int64(5), "five"})
	r5, _ := em1.Reference(typeOf[uAuthor](), 5)
	_, _ = em1.Reference(typeOf[uAuthor](), 6)
	entity.Touch(r5)
	if !strings.Contains(rec1.Queries()[0], `"id" = $1`) {
		t.Fatalf("query = %v", rec1.Queries())
	}

	// Missing rows, closed and detached managers panic with LazyLoadError.
	em2, _ := newUnitEM(t, dialect.Postgres)
	missing, _ := em2.Reference(typeOf[uAuthor](), 9)
	var le *LazyLoadError
	if err := panicErr(func() { entity.Touch(missing) }); !errors.As(err, &le) || !errors.Is(err, ErrNotFound) || le.Entity != "uAuthor" {
		t.Fatalf("missing = %v", err)
	}
	if !strings.Contains(le.Error(), "uAuthor(9)") {
		t.Fatal(le.Error())
	}
	em2.closed = true
	if err := panicErr(func() { entity.Touch(missing) }); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed = %v", err)
	}
	em3, _ := newUnitEM(t, dialect.Postgres)
	det, _ := em3.Reference(typeOf[uAuthor](), 3)
	em3.Clear()
	if err := panicErr(func() { entity.Touch(det) }); !errors.Is(err, ErrDetached) {
		t.Fatalf("detached = %v", err)
	}
	if err := Load(ctx, det); !errors.Is(err, ErrDetached) {
		t.Fatalf("Load detached = %v", err)
	}
}

func TestLoadAndCatch(t *testing.T) {
	ctx := context.Background()
	if Load(ctx, 42) != nil || Load(ctx, &uAuthor{}) != nil {
		t.Fatal("non references load nothing")
	}
	em, rec := newUnitEM(t, dialect.Postgres)
	rec.Returns("FROM", []string{"id", "name"}, []driver.Value{int64(1), "x"})
	r, _ := em.Reference(typeOf[uAuthor](), 1)
	if err := Load(ctx, r); err != nil || r.(*uAuthor).Name != "x" || Load(ctx, r) != nil {
		t.Fatal("Load")
	}
	missing, _ := em.Reference(typeOf[uAuthor](), 2)
	rec.Reset()
	em2, _ := newUnitEM(t, dialect.Postgres)
	m2, _ := em2.Reference(typeOf[uAuthor](), 2)
	if err := Load(ctx, m2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load missing = %v", err)
	}
	_ = missing
	if err := CatchLazy(func() error { return errors.New("plain") }); err == nil || err.Error() != "plain" {
		t.Fatal("plain errors pass through")
	}
	if err := CatchLazy(func() error { panic(&LazyLoadError{Err: ErrNotFound}) }); !errors.Is(err, ErrNotFound) {
		t.Fatal("lazy panic converted")
	}
	if err := panicErr(func() { _ = CatchLazy(func() error { panic(errors.New("other")) }) }); err == nil || err.Error() != "other" {
		t.Fatal("other panics are re-raised")
	}
}

func TestWithoutLazyLoading(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.Postgres)
	if err := em.reg.Register(qAuthor{}); err != nil {
		t.Fatal(err)
	}
	WithoutLazyLoading()(em)
	var loads []LazyLoad
	WithLazyLoadHook(func(l LazyLoad) { loads = append(loads, l) })(em)
	ref, _ := em.Reference(typeOf[qAuthor](), 1)
	err := panicErr(func() { entity.Touch(ref) })
	if !errors.Is(err, ErrLazyLoadingDisabled) || !strings.Contains(err.Error(), "With(") {
		t.Fatalf("touch = %v", err)
	}
	// Caller skips tenet frames, which includes this test.
	if len(loads) != 1 || loads[0].Entity != "qAuthor" || loads[0].ID != int64(1) || loads[0].Caller.Function != "testing.tRunner" {
		t.Fatalf("hook = %+v", loads)
	}

	// Load is explicit and still loads; the collection is not.
	rec.Returns(`FROM "qa"`, []string{"id", "name"}, []driver.Value{int64(1), "Ann"})
	if err := Load(ctx, ref); err != nil {
		t.Fatal(err, rec.Queries())
	}
	a := ref.(*qAuthor)
	if a.Name != "Ann" || len(loads) != 1 {
		t.Fatalf("Load: %q %d", a.Name, len(loads))
	}
	err = panicErr(func() { a.Books.All() })
	if !errors.Is(err, ErrLazyLoadingDisabled) || !strings.Contains(err.Error(), "qAuthor.Books") || len(loads) != 2 {
		t.Fatalf("collection = %v %+v", err, loads)
	}
}
