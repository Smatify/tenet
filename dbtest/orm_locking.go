package dbtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

func init() { Register(Case{"ORM/Locking", testORMLocking}) }

type lkAccount struct {
	smorm.Entity `smorm:"table:lk_account"`
	ID           int64
	Balance      int64
	Version      int64 `smorm:"version"`
}

type lkDoc struct {
	smorm.Entity `smorm:"table:lk_doc"`
	ID           int64
	Title        string    `smorm:"length:50"`
	Revision     time.Time `smorm:"version"`
}

func testORMLocking(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, lkAccount{}, lkDoc{})
	em := newEM()
	acc := &lkAccount{Balance: 100}
	doc := &lkDoc{Title: "draft"}
	must(t, em.Persist(acc))
	must(t, em.Persist(doc))
	must(t, em.Flush(ctx))
	if acc.Version != 1 || doc.Revision.IsZero() {
		t.Fatalf("initial versions %d %v", acc.Version, doc.Revision)
	}

	a1, err := smorm.Find[lkAccount](ctx, newEM(), acc.ID)
	must(t, err)
	em2 := newEM()
	a2, err := smorm.Find[lkAccount](ctx, em2, acc.ID)
	must(t, err)
	// First writer wins.
	em1 := smorm.NewEntityManager(db, smorm.WithRegistry(em.Registry()))
	a1, err = smorm.Find[lkAccount](ctx, em1, acc.ID)
	must(t, err)
	a1.Balance = 50
	must(t, em1.Flush(ctx))
	if a1.Version != 2 {
		t.Fatalf("version after update = %d", a1.Version)
	}
	a2.Balance = 70
	err = em2.Flush(ctx)
	var ole *smorm.OptimisticLockError
	if !errors.Is(err, smorm.ErrOptimisticLock) || !errors.As(err, &ole) || ole.Entity != "lkAccount" {
		t.Fatalf("expected optimistic lock error, got %v", err)
	}
	check, err := smorm.Find[lkAccount](ctx, newEM(), acc.ID)
	must(t, err)
	if check.Balance != 50 || check.Version != 2 {
		t.Fatalf("lost update: %+v", check)
	}
	// No change, no version bump.
	must(t, em1.Flush(ctx))
	if a1.Version != 2 {
		t.Fatal("version must not change without changes")
	}
	// Time based versions.
	d1, err := smorm.Find[lkDoc](ctx, newEM(), doc.ID)
	must(t, err)
	emD := newEM()
	d2, err := smorm.Find[lkDoc](ctx, emD, doc.ID)
	must(t, err)
	emD1 := smorm.NewEntityManager(db, smorm.WithRegistry(em.Registry()), smorm.WithClock(func() time.Time { return time.Now().Add(time.Second) }))
	d1, err = smorm.Find[lkDoc](ctx, emD1, doc.ID)
	must(t, err)
	d1.Title = "final"
	must(t, emD1.Flush(ctx))
	d2.Title = "other"
	if err := emD.Flush(ctx); !errors.Is(err, smorm.ErrOptimisticLock) {
		t.Fatalf("time version conflict: %v", err)
	}
	// Explicit optimistic lock check.
	em3 := newEM()
	a3, err := smorm.Find[lkAccount](ctx, em3, acc.ID)
	must(t, err)
	must(t, em3.Lock(ctx, a3, smorm.LockOptimistic, int64(2)))
	if err := em3.Lock(ctx, a3, smorm.LockOptimistic, int64(1)); !errors.Is(err, smorm.ErrOptimisticLock) {
		t.Fatalf("Lock optimistic = %v", err)
	}
	// Pessimistic locks inside a transaction.
	if _, err := smorm.FindLocked[lkAccount](ctx, newEM(), smorm.LockPessimisticWrite, acc.ID); err == nil {
		t.Fatal("pessimistic lock outside a transaction must fail")
	}
	must(t, newEM().Transactional(ctx, func(ctx context.Context, em *smorm.EntityManager) error {
		locked, err := smorm.FindLocked[lkAccount](ctx, em, smorm.LockPessimisticWrite, acc.ID)
		if err != nil {
			return err
		}
		if err := em.Lock(ctx, locked, smorm.LockPessimisticRead, nil); err != nil {
			return err
		}
		locked.Balance += 1
		return nil
	}))
	check, err = smorm.Find[lkAccount](ctx, newEM(), acc.ID)
	must(t, err)
	if check.Balance != 51 || check.Version != 3 {
		t.Fatalf("after locked update: %+v", check)
	}
	if err := newEM().Lock(ctx, check, smorm.LockOptimistic, 1); err == nil {
		t.Fatal("Lock on unmanaged entity")
	}
}
