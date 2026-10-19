package orm

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/internal/fakedb"
	"github.com/smatify/smorm/internal/testdialect"
	"github.com/smatify/smorm/mapping"
)

type uAuthor struct {
	entity.Header `smorm:"table:a"`
	ID            int64
	Name          string
}

type uBook struct {
	entity.Header `smorm:"table:b"`
	ID            string   `smorm:"generated:none"`
	Author        *uAuthor `smorm:"notnull"`
	Prev          *uBook
}

type uNode struct {
	entity.Header `smorm:"table:n"`
	ID            int64
	Next          *uNode `smorm:"notnull"`
}

type stamped struct {
	entity.Header `smorm:"table:s"`
	ID            int64
	Name          string
	created       time.Time
	updated       time.Time
}

func (s *stamped) TouchCreated(now time.Time) { s.created, s.updated = now, now }
func (s *stamped) TouchUpdated(now time.Time) { s.updated = now }

func newUnitEM(t *testing.T, n dialect.Name) (*EntityManager, *fakedb.Recorder) {
	t.Helper()
	rec := fakedb.New()
	db := dbal.NewDB(rec.DB(), testdialect.Like(n))
	t.Cleanup(func() { _ = db.Close() })
	reg := mapping.NewRegistry()
	if err := reg.Register(uAuthor{}, uBook{}, uNode{}, stamped{}); err != nil {
		t.Fatal(err)
	}
	return New(db, WithRegistry(reg), WithClock(func() time.Time { return time.Unix(100, 0) })), rec
}

func TestFlushSQLAndOrder(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.MySQL) // LastInsertId path
	a := &uAuthor{Name: "a"}
	b2 := &uBook{ID: "b2", Author: a}
	b1 := &uBook{ID: "b1", Author: a}
	b2.Prev = b1
	b1.Prev = b2 // cycle through nullable Prev
	for _, p := range []any{b2, b1, a} {
		if err := em.Persist(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := em.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	q := rec.Queries()
	want := []string{
		"BEGIN",
		"INSERT INTO `a` (`name`) VALUES (?)",
		"INSERT INTO `b` (`id`, `author_id`, `prev_id`) VALUES (?, ?, ?)",
		"INSERT INTO `b` (`id`, `author_id`, `prev_id`) VALUES (?, ?, ?)",
		"UPDATE `b` SET `prev_id` = ? WHERE (`id` = ?)",
		"COMMIT",
	}
	if !slices.Equal(q, want) {
		t.Fatalf("got\n%s", strings.Join(q, "\n"))
	}
	if a.ID != 1 {
		t.Fatal("identity from LastInsertId")
	}
	// The first book is inserted with NULL prev and fixed by the deferred update.
	calls := rec.Calls()
	if calls[2].Args[2] != nil || calls[3].Args[2] != calls[2].Args[0] || calls[4].Args[1] != calls[2].Args[0] {
		t.Fatalf("args = %v / %v", calls[2].Args, calls[4].Args)
	}
	rec.Reset()
	if err := em.Flush(ctx); err != nil || len(rec.Queries()) != 0 {
		t.Fatalf("idle flush = %v %v", err, rec.Queries())
	}
}

func TestNotNullCycleFails(t *testing.T) {
	em, _ := newUnitEM(t, dialect.Postgres)
	x, y := &uNode{}, &uNode{}
	x.Next, y.Next = y, x
	_ = em.Persist(x)
	_ = em.Persist(y)
	if err := em.Flush(context.Background()); err == nil || !strings.Contains(err.Error(), "NOT NULL") {
		t.Fatalf("err = %v", err)
	}
}

func TestReturningAndTimestamps(t *testing.T) {
	em, rec := newUnitEM(t, dialect.Postgres)
	rec.Returns("RETURNING", []string{"id"}, []driver.Value{int64(42)})
	s := &stamped{Name: "x"}
	if err := em.Persist(s); err != nil {
		t.Fatal(err)
	}
	if err := em.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.ID != 42 || s.created.Unix() != 100 {
		t.Fatalf("s = %+v", s)
	}
	got, err := em.Find(context.Background(), typeOf[stamped](), 42)
	if err != nil || got != any(s) {
		t.Fatal("identity map by int key")
	}
	em.now = func() time.Time { return time.Unix(200, 0) }
	s.Name = "y"
	rec.Reset()
	if err := em.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.updated.Unix() != 200 || s.created.Unix() != 100 {
		t.Fatal("TouchUpdated")
	}
	if q := rec.Queries(); len(q) != 3 || !strings.Contains(q[1], `"updated"`) || !strings.Contains(q[1], `"name"`) {
		t.Fatalf("update = %v", q)
	}
}

func TestErrorsAndStates(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.SQLite)
	if StateRemoved.String() != "removed" || em.DB() == nil || em.Registry() == nil || !em.IsOpen() {
		t.Fatal("accessors")
	}
	if err := em.Persist(42); !errors.Is(err, ErrNotEntity) {
		t.Fatal(err)
	}
	if err := em.Remove(nil); !errors.Is(err, ErrNotEntity) {
		t.Fatal(err)
	}
	if err := em.Detach("x"); !errors.Is(err, ErrNotEntity) {
		t.Fatal(err)
	}
	if em.Contains(1) || em.State(1) != StateNew || !em.IsInitialized(&uAuthor{}) || em.IsInitialized(1) {
		t.Fatal("non entities")
	}
	if err := em.Persist(&uBook{}); err == nil {
		t.Fatal("application id required")
	}
	if err := em.Remove(&uAuthor{}); err != nil {
		t.Fatal("removing unknown is a no-op")
	}
	if _, err := em.Find(ctx, typeOf[uAuthor]()); err == nil {
		t.Fatal("id count")
	}
	if _, err := em.Find(ctx, typeOf[int]()); err == nil {
		t.Fatal("bad type")
	}
	if _, err := em.Reference(typeOf[int]()); err == nil {
		t.Fatal("bad type")
	}
	if err := em.Initialize(ctx, 3); err == nil {
		t.Fatal("bad init")
	}
	if err := em.Initialize(ctx, &uAuthor{}); err != nil {
		t.Fatal("unknown entity initialize is a no-op")
	}
	ref, _ := em.Reference(typeOf[uAuthor](), 7)
	if em.IsInitialized(ref) || em.State(ref) != StateManaged {
		t.Fatal("reference")
	}
	if err := em.Initialize(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing row: %v", err)
	}
	// A failing insert closes the entity manager.
	rec.FailOn("INSERT", errors.New("boom"))
	_ = em.Persist(&uAuthor{Name: "x"})
	if err := em.Flush(ctx); err == nil || em.IsOpen() {
		t.Fatal("flush must fail and close")
	}
	for _, err := range []error{em.Persist(&uAuthor{}), em.Remove(&uAuthor{}), em.Flush(ctx), em.Refresh(ctx, &uAuthor{}),
		em.Transactional(ctx, nil)} {
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("expected ErrClosed, got %v", err)
		}
	}
	if _, err := em.Find(ctx, typeOf[uAuthor](), 1); !errors.Is(err, ErrClosed) {
		t.Fatal("find after close")
	}
	other, _ := newUnitEM(t, dialect.SQLite)
	a := &uAuthor{}
	_ = other.Persist(a)
	em2, _ := newUnitEM(t, dialect.SQLite)
	if err := em2.Persist(a); err == nil {
		t.Fatal("entity of another manager")
	}
	if e := (&NewEntityFoundError{Source: "A", Assoc: "b", Target: "B"}).Error(); !strings.Contains(e, "A.b") {
		t.Fatal(e)
	}
}

func TestHelpers(t *testing.T) {
	if idKey([]any{int32(5)}) != idKey([]any{int64(5)}) || idKey([]any{uint8(5)}) != int64(5) {
		t.Fatal("int keys normalize")
	}
	s := "x"
	if idKey([]any{&s}) != "x" || idKey([]any{(*string)(nil)}) != nil || idKey([]any{nil}) != nil {
		t.Fatal("pointer keys")
	}
	if idKey([]any{1, "a"}) != "1\x00a" {
		t.Fatal("composite key")
	}
	if !equalDB(nil, nil) || equalDB(nil, 1) || !equalDB([]byte("a"), []byte("a")) || equalDB([]byte("a"), "a") ||
		!equalDB(time.Unix(1, 0), time.Unix(1, 0).UTC()) || equalDB(1, int64(1)) || !equalDB([]int{1}, []int{1}) || !equalDB("a", "a") {
		t.Fatal("equalDB")
	}
	if convertTo(nil, typeOf[int]()) != nil || convertTo(5, typeOf[int64]()) != int64(5) || convertTo("x", typeOf[int]()) != "x" {
		t.Fatal("convertTo")
	}
	if !isZeroID(nil) || !isZeroID(0) || isZeroID(1) {
		t.Fatal("isZeroID")
	}
	for n, want := range map[dialect.Name]string{
		dialect.Postgres: `SELECT nextval('"s"')`, dialect.MSSQL: "SELECT NEXT VALUE FOR [s]", dialect.MariaDB: "SELECT NEXTVAL(`s`)",
	} {
		if got := nextValSQL(testdialect.Like(n), "s"); got != want {
			t.Errorf("%s: %s", n, got)
		}
	}
}

func TestBatchInsert(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.Postgres)
	rec.Returns("RETURNING", []string{"id"}, []driver.Value{int64(1)})
	a := &uAuthor{Name: "a"}
	_ = em.Persist(a)
	for _, id := range []string{"x", "y", "z"} {
		if err := em.Persist(&uBook{ID: id, Author: a}); err != nil {
			t.Fatal(err)
		}
	}
	if err := em.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	q := rec.Queries()
	if len(q) != 4 || !strings.Contains(q[2], `VALUES ($1, $2, $3), ($4, $5, $6), ($7, $8, $9)`) {
		t.Fatalf("queries:\n%s", strings.Join(q, "\n"))
	}
	if len(rec.Calls()[2].Args) != 9 {
		t.Fatal("args")
	}
	// Chunking respects the bind parameter limit.
	em2, rec2 := newUnitEM(t, dialect.Postgres)
	rec2.Returns("RETURNING", []string{"id"}, []driver.Value{int64(1)})
	b := &uAuthor{Name: "b"}
	_ = em2.Persist(b)
	for i := range 1500 {
		_ = em2.Persist(&uBook{ID: fmt.Sprint("id", i), Author: b})
	}
	if err := em2.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	inserts := 0
	for _, s := range rec2.Queries() {
		if strings.HasPrefix(s, `INSERT INTO "b"`) {
			inserts++
		}
	}
	if inserts != 3 { // 3 columns -> 500 rows per statement
		t.Fatalf("inserts = %d", inserts)
	}
}
