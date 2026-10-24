package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
)

type ormNoTable struct {
	smorm.Entity `smorm:"table:orm_no_table"`
	smorm.UUIDID
}

func init() {
	Register(Case{"ORM/LifecycleHooksAndEvents", testORMEvents}, Case{"ORM/PostFlushAfterCommit", testORMPostFlushAfterCommit})
}

type ormHooked struct {
	smorm.Entity `smorm:"table:orm_hooked"`
	ID           int64
	Title        string `smorm:"length:100"`
	Slug         string `smorm:"length:100"`
	loads        int    `smorm:"-"`
}

func (h *ormHooked) PrePersist(context.Context) error { h.Slug = strings.ToLower(h.Title); return nil }
func (h *ormHooked) PreUpdate(_ context.Context, cs *smorm.ChangeSet) error {
	if cs.Has("Title") {
		h.Slug = strings.ToLower(h.Title)
	}
	return nil
}
func (h *ormHooked) PostLoad(context.Context) error { h.loads++; return nil }

func testORMEvents(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, ormHooked{})
	events := smorm.NewEventManager()
	counts := map[smorm.Event]int{}
	events.On(func(_ context.Context, a *smorm.EventArgs) error {
		counts[smorm.EventPostPersist]++
		return nil
	}, smorm.EventPostPersist)
	events.On(func(_ context.Context, a *smorm.EventArgs) error {
		counts[smorm.EventPostFlush]++
		return nil
	}, smorm.EventPostFlush)
	em := newEM()
	em2 := smorm.NewEntityManager(db, smorm.WithRegistry(em.Registry()), smorm.WithEvents(events))
	h := &ormHooked{Title: "Hello World"}
	must(t, em2.Persist(h))
	must(t, em2.Flush(ctx))
	if counts[smorm.EventPostPersist] != 1 || counts[smorm.EventPostFlush] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	loaded, err := smorm.Find[ormHooked](ctx, newEM(), h.ID)
	must(t, err)
	if loaded.Slug != "hello world" || loaded.loads != 1 {
		t.Fatalf("loaded = %+v", loaded)
	}
	h.Title = "Changed"
	must(t, em2.Flush(ctx))
	again, err := smorm.Find[ormHooked](ctx, newEM(), h.ID)
	must(t, err)
	if again.Slug != "changed" {
		t.Fatalf("PreUpdate change not written: %+v", again)
	}
}

// postFlush follows the commit, also in Transactional; rolled back writes
// are followed by onRollback instead.
func testORMPostFlushAfterCommit(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, ormHooked{})
	events := smorm.NewEventManager()
	var log []string
	events.On(func(_ context.Context, a *smorm.EventArgs) error {
		log = append(log, "postPersist")
		return nil
	}, smorm.EventPostPersist)
	events.On(func(_ context.Context, a *smorm.EventArgs) error {
		log = append(log, "postFlush")
		return nil
	}, smorm.EventPostFlush)
	events.On(func(_ context.Context, a *smorm.EventArgs) error {
		log = append(log, "onRollback")
		return nil
	}, smorm.EventOnRollback)
	reg := newEM().Registry()
	em := smorm.NewEntityManager(db, smorm.WithRegistry(reg), smorm.WithEvents(events))
	must(t, em.Transactional(ctx, func(ctx context.Context, em *smorm.EntityManager) error {
		must(t, em.Persist(&ormHooked{Title: "a"}))
		must(t, em.Flush(ctx))
		must(t, em.Persist(&ormHooked{Title: "b"}))
		log = append(log, "fn")
		return nil
	}))
	if got := strings.Join(log, ","); got != "postPersist,fn,postPersist,postFlush,postFlush" {
		t.Fatalf("committed: %s", got)
	}

	log = nil
	err := em.Transactional(ctx, func(ctx context.Context, em *smorm.EntityManager) error {
		must(t, em.Persist(&ormHooked{Title: "c"}))
		must(t, em.Flush(ctx))
		return context.Canceled
	})
	if err != context.Canceled || strings.Join(log, ",") != "postPersist,onRollback" {
		t.Fatalf("rolled back: %v %v", err, log)
	}

	log = nil
	missing := mapping.NewRegistry() // its table does not exist
	must(t, missing.Register(ormNoTable{}))
	failing := smorm.NewEntityManager(db, smorm.WithRegistry(missing), smorm.WithEvents(events))
	must(t, failing.Persist(&ormNoTable{}))
	if err := failing.Flush(ctx); err == nil || strings.Join(log, ",") != "onRollback" {
		t.Fatalf("failed flush: %v %v", err, log)
	}
}
