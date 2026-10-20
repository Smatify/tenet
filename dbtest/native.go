package dbtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/uuid"
)

func init() { Register(Case{"ORM/NativeQueries", testNative}) }

type nqItem struct {
	smorm.Entity `smorm:"table:nq_item"`
	ID           int64
	Name         string `smorm:"length:30"`
	Active       bool
	Price        float64
	Made         time.Time
	Ref          uuid.UUID
	Meta         map[string]any `smorm:"nullable"`
}

func testNative(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, nqItem{}, inSportsCar{}, inTruck{}, inParking{})
	em := newEM()
	made := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, n := range []string{"alpha", "beta", "gamma"} {
		must(t, em.Persist(&nqItem{Name: n, Active: i%2 == 0, Price: float64(i) + 0.5, Made: made, Ref: uuid.NewV7(), Meta: map[string]any{"i": float64(i)}}))
	}
	car := &inCar{inVehicle: inVehicle{Wheels: 4}, Doors: 5}
	truck := &inTruck{inVehicle: inVehicle{Wheels: 6}, Load: 9}
	must(t, em.Persist(car))
	must(t, em.Persist(truck))
	must(t, em.Flush(ctx))

	q := db.Dialect().QuoteIdent
	em2 := newEM()
	items, err := smorm.Native[nqItem](ctx, em2, nil,
		"SELECT * FROM "+q("nq_item")+" WHERE "+q("price")+" > ? ORDER BY "+q("name")+" DESC", 1)
	must(t, err)
	if len(items) != 2 || items[0].Name != "gamma" || !items[0].Active || items[1].Active || !items[0].Made.Equal(made) || items[0].Meta["i"] != 2.0 || items[0].Ref.IsZero() {
		t.Fatalf("items = %+v", items)
	}
	same, err := smorm.Find[nqItem](ctx, em2, items[0].ID)
	must(t, err)
	if same != items[0] {
		t.Fatal("native results must go through the identity map")
	}
	// Renamed columns.
	renamed, err := smorm.Native[nqItem](ctx, newEM(), &smorm.ResultSetMapping{Rename: map[string]string{"item_name": "name"}},
		"SELECT "+q("id")+", "+q("name")+" AS "+q("item_name")+", "+q("active")+", "+q("price")+", "+q("made")+", "+q("ref")+", "+q("meta")+
			" FROM "+q("nq_item")+" WHERE "+q("name")+" = ?", "alpha")
	must(t, err)
	if len(renamed) != 1 || renamed[0].Name != "alpha" {
		t.Fatalf("renamed = %+v", renamed)
	}
	if _, err := smorm.Native[nqItem](ctx, newEM(), nil, "SELECT "+q("id")+" FROM "+q("nq_item")); err == nil || !strings.Contains(err.Error(), "missing columns") {
		t.Fatalf("missing columns: %v", err)
	}
	// Polymorphic native query.
	vs, err := smorm.Native[inVehicle](ctx, newEM(), nil, "SELECT * FROM "+q("in_vehicle")+" ORDER BY "+q("id"))
	must(t, err)
	if len(vs) != 2 {
		t.Fatalf("vehicles = %d", len(vs))
	}
	if c, ok := smorm.Concrete(vs[0]).(*inCar); !ok || c.Doors != 5 {
		t.Fatalf("first vehicle = %T", smorm.Concrete(vs[0]))
	}
	if tr, ok := smorm.Concrete(vs[1]).(*inTruck); !ok || tr.Load != 9 {
		t.Fatalf("second vehicle = %T", smorm.Concrete(vs[1]))
	}
	// Scalars.
	rows, err := newEM().NativeScalars(ctx, "SELECT COUNT(*) AS n, MAX("+q("name")+") AS "+q("top")+" FROM "+q("nq_item"))
	must(t, err)
	if len(rows) != 1 || rows[0]["top"] != "gamma" {
		t.Fatalf("scalars = %v", rows)
	}
}
