package dbtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/cache"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/uuid"
)

func init() { Register(Case{"ORM/SecondLevelCache", testORMCache}) }

type slcCategory struct {
	tenet.Entity `tenet:"table:slc_category,cache"`
	ID           int64
	Name         string `tenet:"length:30"`
}

type slcProduct struct {
	tenet.Entity `tenet:"table:slc_product,cache,cacheTTL:1h"`
	tenet.SoftDelete
	ID       int64
	Name     string `tenet:"length:30"`
	Active   bool
	Price    float64
	Made     time.Time
	Meta     map[string]any `tenet:"nullable"`
	Ref      uuid.UUID
	Note     *string      `tenet:"length:30"`
	TenantID int64        `tenet:"column:tenant_id"`
	Category *slcCategory `tenet:"manyToOne"`
}

func testORMCache(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, slcProduct{})
	reg := newEM().Registry()
	c := cache.NewMemory(100)
	em := func() *tenet.EntityManager {
		return tenet.NewEntityManager(db, tenet.WithRegistry(reg), tenet.WithSecondLevelCache(c), tenet.WithContext(ctx))
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
	if _, err := tenet.Find[slcProduct](ctx, em(), p.ID); err != nil {
		t.Fatal(err)
	}
	if selects != 1 {
		t.Fatalf("first find selects = %d", selects)
	}
	e2 := em()
	got, err := tenet.Find[slcProduct](ctx, e2, p.ID)
	must(t, err)
	if selects != 1 {
		t.Fatalf("cached find must not query, selects = %d", selects)
	}
	if got.Name != "hammer" || !got.Active || got.Price != 9.5 || !got.Made.Equal(made) || got.Meta["a"] != 1.0 || got.Ref != p.Ref || got.Note != nil || got.TenantID != 1 {
		t.Fatalf("cached entity = %+v", got)
	}
	// The category reference was not cached yet: loading it queries once,
	// afterwards other entity managers get it from the cache.
	must(t, tenet.Load(ctx, got.Category))
	if got.Category.Name != "tools" {
		t.Fatal("lazy category")
	}
	before := selects
	ref, err := tenet.Reference[slcCategory](em(), cat.ID)
	must(t, err)
	must(t, tenet.Load(ctx, ref))
	if ref.Name != "tools" || selects != before {
		t.Fatal("category reference must be initialized from the cache")
	}
	// Updates refresh the cache after commit.
	got.Name = "sledgehammer"
	must(t, e2.Flush(ctx))
	again, err := tenet.Find[slcProduct](ctx, em(), p.ID)
	must(t, err)
	if again.Name != "sledgehammer" || selects != before {
		t.Fatalf("updated cached = %q, selects %d", again.Name, selects)
	}
	// Filters other than soft delete bypass the cache.
	e3 := em()
	e3.Filters().Enable("tenant", tenet.ColumnFilter("tenant_id", 2))
	if _, err := tenet.Find[slcProduct](ctx, e3, p.ID); !errors.Is(err, tenet.ErrNotFound) || selects == before {
		t.Fatalf("tenant filter must bypass the cache: %v", err)
	}
	// Removal evicts.
	e4 := em()
	rm, err := tenet.Find[slcProduct](ctx, e4, p.ID)
	must(t, err)
	must(t, e4.Remove(rm))
	must(t, e4.Flush(ctx))
	if _, err := tenet.Find[slcProduct](ctx, em(), p.ID); !errors.Is(err, tenet.ErrNotFound) {
		t.Fatalf("soft deleted product served: %v", err)
	}
	if c.Stats().Hits == 0 {
		t.Fatal("no cache hits recorded")
	}
}
