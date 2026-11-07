package orm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smatify/tenet/dialect"
	"github.com/smatify/tenet/mapping"
)

func TestNextVersion(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)
	cases := []struct {
		f   *mapping.Field
		cur any
		out any
	}{
		{&mapping.Field{GoType: typeOf[int64]()}, int64(4), int64(5)},
		{&mapping.Field{GoType: typeOf[int32]()}, int32(0), int32(1)},
		{&mapping.Field{GoType: typeOf[uint]()}, uint(9), uint(10)},
		{&mapping.Field{GoType: typeOf[time.Time]()}, time.Time{}, now.Truncate(time.Microsecond)},
		{&mapping.Field{GoType: typeOf[string]()}, "x", "x"},
	}
	for _, c := range cases {
		if got := nextVersion(c.f, c.cur, now); got != c.out {
			t.Errorf("nextVersion(%v) = %v", c.cur, got)
		}
	}
	e := &OptimisticLockError{Entity: "A", ID: 1, Version: 2}
	if !errors.Is(e, ErrOptimisticLock) || !strings.Contains(e.Error(), "A(1)") {
		t.Fatal("error")
	}
	if dbalLock(LockPessimisticRead) == dbalLock(LockPessimisticWrite) || !asTime(1).IsZero() {
		t.Fatal("helpers")
	}
}

func TestLockErrors(t *testing.T) {
	ctx := context.Background()
	em, _ := newUnitEM(t, dialect.Postgres)
	if err := em.Lock(ctx, 1, LockOptimistic, 1); err == nil {
		t.Fatal("not an entity")
	}
	r, _ := em.Reference(typeOf[uAuthor](), 1)
	if err := em.Lock(ctx, r, LockOptimistic, 1); err == nil || !strings.Contains(err.Error(), "version field") {
		t.Fatalf("no version: %v", err)
	}
	if err := em.Lock(ctx, r, LockPessimisticWrite, nil); err == nil {
		t.Fatal("outside tx")
	}
	if err := em.Lock(ctx, r, LockMode(99), nil); err == nil {
		t.Fatal("unknown mode")
	}
	if _, err := em.FindLocked(ctx, typeOf[uAuthor](), LockOptimistic, 1); err == nil {
		t.Fatal("optimistic FindLocked")
	}
	if _, err := em.FindLocked(ctx, typeOf[uAuthor](), LockPessimisticWrite, 1); err == nil {
		t.Fatal("FindLocked outside tx")
	}
	em.closed = true
	if _, err := em.FindLocked(ctx, typeOf[uAuthor](), LockPessimisticWrite, 1); !errors.Is(err, ErrClosed) {
		t.Fatal("closed")
	}
}
