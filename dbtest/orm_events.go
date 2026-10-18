package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

func init() { Register(Case{"ORM/LifecycleHooksAndEvents", testORMEvents}) }

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
