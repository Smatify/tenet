package dbal

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/fakedb"
	"github.com/smatify/smorm/internal/testdialect"
)

func newTestDB(t *testing.T, opts ...Option) (*DB, *fakedb.Recorder) {
	t.Helper()
	rec := fakedb.New()
	db := NewDB(rec.DB(), testdialect.New(), opts...)
	t.Cleanup(func() { _ = db.Close() })
	return db, rec
}

func TestExecQueryAndHooks(t *testing.T) {
	var mu sync.Mutex
	var events []string
	hook := QueryHookFuncs{
		Before: func(ctx context.Context, ev *QueryEvent) context.Context {
			mu.Lock()
			events = append(events, "before:"+ev.Operation)
			mu.Unlock()
			return context.WithValue(ctx, ctxKey{}, "v")
		},
		After: func(ctx context.Context, ev *QueryEvent) {
			mu.Lock()
			defer mu.Unlock()
			if ctx.Value(ctxKey{}) != "v" {
				t.Error("context from BeforeQuery not propagated")
			}
			if ev.Duration() < 0 {
				t.Error("negative duration")
			}
			events = append(events, "after:"+ev.Operation)
		},
	}
	db, rec := newTestDB(t, WithQueryHook(hook))
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES (?)", 1); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, "  select 1")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	_ = db.QueryRowContext(ctx, "(SELECT 2)").Scan(new(int))

	want := []string{"before:INSERT", "after:INSERT", "before:SELECT", "after:SELECT", "before:SELECT", "after:SELECT"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v", events)
	}
	if got := rec.Calls()[0].Args; len(got) != 1 || got[0] != 1 {
		t.Fatalf("args = %v", got)
	}
}

type ctxKey struct{}

func TestHookOrderAndErrors(t *testing.T) {
	var order []string
	mk := func(name string) QueryHook {
		return QueryHookFuncs{
			Before: func(ctx context.Context, _ *QueryEvent) context.Context { order = append(order, "b"+name); return ctx },
			After:  func(_ context.Context, ev *QueryEvent) { order = append(order, "a"+name); _ = ev.Err },
		}
	}
	db, rec := newTestDB(t, WithQueryHook(mk("1")))
	db.AddQueryHook(mk("2"))
	db.AddQueryHook(QueryHookFuncs{}) // nil funcs are fine
	boom := errors.New("boom")
	rec.FailOn("DELETE", boom)
	var seen error
	db.AddQueryHook(QueryHookFuncs{After: func(_ context.Context, ev *QueryEvent) { seen = ev.Err }})

	if _, err := db.ExecContext(context.Background(), "DELETE FROM t"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(seen, boom) {
		t.Fatalf("hook saw %v", seen)
	}
	if !slices.Equal(order, []string{"b1", "b2", "a2", "a1"}) {
		t.Fatalf("order = %v", order)
	}
}

func TestRunInTxCommitAndRollback(t *testing.T) {
	db, rec := newTestDB(t)
	ctx := context.Background()

	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx *Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT 1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Queries(); !slices.Equal(got, []string{"BEGIN", "INSERT 1", "COMMIT"}) {
		t.Fatalf("got %v", got)
	}

	rec.Reset()
	boom := errors.New("boom")
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx *Tx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if got := rec.Queries(); !slices.Equal(got, []string{"BEGIN", "ROLLBACK"}) {
		t.Fatalf("got %v", got)
	}
}

func TestRunInTxPanicRollsBack(t *testing.T) {
	db, rec := newTestDB(t)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
		if got := rec.Queries(); !slices.Equal(got, []string{"BEGIN", "ROLLBACK"}) {
			t.Fatalf("got %v", got)
		}
	}()
	_ = db.RunInTx(context.Background(), nil, func(context.Context, *Tx) error { panic("x") })
}

func TestNestedTransactionsUseSavepoints(t *testing.T) {
	db, rec := newTestDB(t)
	ctx := context.Background()
	boom := errors.New("boom")

	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx *Tx) error {
		if tx.IsNested() {
			t.Error("root must not be nested")
		}
		if err := tx.RunInTx(ctx, nil, func(ctx context.Context, inner *Tx) error {
			if !inner.IsNested() {
				t.Error("inner must be nested")
			}
			_, err := inner.ExecContext(ctx, "INSERT a")
			return err
		}); err != nil {
			return err
		}
		err := tx.RunInTx(ctx, nil, func(ctx context.Context, inner *Tx) error { return boom })
		if !errors.Is(err, boom) {
			t.Errorf("inner err = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"BEGIN", "SAVEPOINT sp_1", "INSERT a", "RELEASE SAVEPOINT sp_1",
		"SAVEPOINT sp_2", "ROLLBACK TO SAVEPOINT sp_2", "COMMIT"}
	if got := rec.Queries(); !slices.Equal(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestSavepointWithoutReleaseSupport(t *testing.T) {
	rec := fakedb.New()
	d := testdialect.New()
	db := NewDB(rec.DB(), noRelease{d})
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := tx.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := rec.Queries(); !slices.Equal(got, []string{"BEGIN", "SAVEPOINT sp_1", "COMMIT"}) {
		t.Fatalf("got %v", got)
	}
}

type noRelease struct{ *testdialect.Dialect }

func (noRelease) ReleaseSavepointSQL(string) string { return "" }

func TestSavepointUnsupported(t *testing.T) {
	rec := fakedb.New()
	d := testdialect.New()
	d.F &^= dialect.FeatureSavepoint
	db := NewDB(rec.DB(), d)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.BeginTx(context.Background(), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestTxDone(t *testing.T) {
	db, _ := newTestDB(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); !errors.Is(err, ErrTxDone) {
		t.Fatalf("err = %v", err)
	}
	if err := tx.Rollback(); !errors.Is(err, ErrTxDone) {
		t.Fatalf("err = %v", err)
	}
	if _, err := tx.BeginTx(ctx, nil); !errors.Is(err, ErrTxDone) {
		t.Fatalf("err = %v", err)
	}
}

func TestRollbackErrorIsJoined(t *testing.T) {
	db, rec := newTestDB(t)
	ctx := context.Background()
	rbErr := errors.New("rollback failed")
	rec.FailOn("ROLLBACK TO", rbErr)
	boom := errors.New("boom")
	_ = db.RunInTx(ctx, nil, func(ctx context.Context, tx *Tx) error {
		err := tx.RunInTx(ctx, nil, func(context.Context, *Tx) error { return boom })
		if !errors.Is(err, boom) || !errors.Is(err, rbErr) {
			t.Errorf("err = %v", err)
		}
		return nil
	})
}

func TestConn(t *testing.T) {
	db, rec := newTestDB(t)
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.DB() != db || c.Dialect() != db.Dialect() {
		t.Fatal("wrong parent")
	}
	if _, err := c.ExecContext(ctx, "UPDATE x"); err != nil {
		t.Fatal(err)
	}
	rows, err := c.QueryContext(ctx, "SELECT x")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	_ = c.QueryRowContext(ctx, "SELECT y").Scan(new(int))
	if err := c.RunInTx(ctx, nil, func(ctx context.Context, tx *Tx) error {
		_ = tx.QueryRowContext(ctx, "SELECT z").Scan(new(int))
		r, err := tx.QueryContext(ctx, "SELECT w")
		if err == nil {
			r.Close()
		}
		if tx.DB() != db || tx.SQL() == nil || tx.Dialect() == nil {
			t.Error("tx accessors")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"UPDATE x", "SELECT x", "SELECT y", "BEGIN", "SELECT z", "SELECT w", "COMMIT"}
	if got := rec.Queries(); !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestDBAccessors(t *testing.T) {
	db, _ := newTestDB(t)
	if db.SQL() == nil || db.Dialect().Name() != "test" {
		t.Fatal("accessors")
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenUnknownDriver(t *testing.T) {
	if _, err := Open("does-not-exist", "", testdialect.New()); err == nil {
		t.Fatal("expected error")
	}
}

func TestOperation(t *testing.T) {
	tests := map[string]string{"select 1": "SELECT", "\n WITH x AS": "WITH", "(SELECT": "SELECT", "COMMIT": "COMMIT", "": ""}
	for in, want := range tests {
		if got := operation(in); got != want {
			t.Errorf("operation(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConcurrentAddHook(t *testing.T) {
	db, _ := newTestDB(t)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); db.AddQueryHook(QueryHookFuncs{}) }()
		go func() { defer wg.Done(); _, _ = db.ExecContext(context.Background(), "SELECT 1") }()
	}
	wg.Wait()
	if n := len(*db.hooks.Load()); n != 50 {
		t.Fatalf("hooks = %d", n)
	}
}
