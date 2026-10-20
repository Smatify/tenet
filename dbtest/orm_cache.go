package dbtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/cache"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/uuid"
)

func init() { Register(Case{"ORM/SecondLevelCache", testORMCache}) }

type slcCategory struct {
	smorm.Entity `smorm:"table:slc_category,cache"`
	ID           int64
	Name         string `smorm:"length:30"`
}

type slcProduct struct {
	smorm.Entity `smorm:"table:slc_product,cache,cacheTTL:1h"`
	smorm.SoftDelete
	ID       int64
	Name     string `smorm:"length:30"`
	Active   bool
	Price    float64
	Made     time.Time
	Meta     map[string]any `smorm:"nullable"`
	Ref      uuid.UUID
	Note     *string      `smorm:"length:30"`
	TenantID int64        `smorm:"column:tenant_id"`
	Category *slcCategory `smorm:"manyToOne"`
}

func testORMCache(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, slcProduct{})
	reg := newEM().Registry()
	c := cache.NewMemory(100)
	em := func() *smorm.EntityManager {
		return smorm.NewEntityManager(db, smorm.WithRegistry(reg), smorm.WithSecondLevelCache(c), smorm.WithContext(ctx))
	}
	made := time.Date(2026, 10, 20, 8, 30, 0, 123000000, time.UTC)
	cat := &slcCategory{Name: "tools"}
	p := &slcProduct{Name: "hammer", Active: true, Price: 9.5, Made: made, Meta: map[string]any{"a": 1.0}, Ref: uuid.NewV7(), TenantID: 1, Category: cat}
	e1 := em()
	must(t, e1.Persist(cat))
	must(t, e1.Persist(p))
	must(t, e1.Flush(ctx))
	c.Clear()

	selects := 0
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "SELECT" && strings.Contains(ev.Query, "slc_") {
			selects++
		}
	}})
	if _, err := smorm.Find[slcProduct](ctx, em(), p.ID); err != nil {
		t.Fatal(err)
	}
	if selects != 1 {
		t.Fatalf("first find selects = %d", selects)
	}
	e2 := em()
	got, err := smorm.Find[slcProduct](ctx, e2, p.ID)
	must(t, err)
	if selects != 1 {
		t.Fatalf("cached find must not query, selects = %d", selects)
	}
	if got.Name != "hammer" || !got.Active || got.Price != 9.5 || !got.Made.Equal(made) || got.Meta["a"] != 1.0 || got.Ref != p.Ref || got.Note != nil || got.TenantID != 1 {
		t.Fatalf("cached entity = %+v", got)
	}
	// The category reference was not cached yet: loading it queries once,
	// afterwards other entity managers get it from the cache.
	must(t, smorm.Load(ctx, got.Category))
	if got.Category.Name != "tools" {
		t.Fatal("lazy category")
	}
	before := selects
	ref, err := smorm.Reference[slcCategory](em(), cat.ID)
	must(t, err)
	must(t, smorm.Load(ctx, ref))
	if ref.Name != "tools" || selects != before {
		t.Fatal("category reference must be initialized from the cache")
	}
	// Updates refresh the cache after commit.
	got.Name = "sledgehammer"
	must(t, e2.Flush(ctx))
	again, err := smorm.Find[slcProduct](ctx, em(), p.ID)
	must(t, err)
	if again.Name != "sledgehammer" || selects != before {
		t.Fatalf("updated cached = %q, selects %d", again.Name, selects)
	}
	// Filters other than soft delete bypass the cache.
	e3 := em()
	e3.Filters().Enable("tenant", smorm.ColumnFilter("tenant_id", 2))
	if _, err := smorm.Find[slcProduct](ctx, e3, p.ID); !errors.Is(err, smorm.ErrNotFound) || selects == before {
		t.Fatalf("tenant filter must bypass the cache: %v", err)
	}
	// Removal evicts.
	e4 := em()
	rm, err := smorm.Find[slcProduct](ctx, e4, p.ID)
	must(t, err)
	must(t, e4.Remove(rm))
	must(t, e4.Flush(ctx))
	if _, err := smorm.Find[slcProduct](ctx, em(), p.ID); !errors.Is(err, smorm.ErrNotFound) {
		t.Fatalf("soft deleted product served: %v", err)
	}
	if c.Stats().Hits == 0 {
		t.Fatal("no cache hits recorded")
	}
}
