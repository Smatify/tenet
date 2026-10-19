package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

func init() { Register(Case{"ORM/Embeddables", testORMEmbedded}) }

type emGeo struct{ Lat, Lng float64 }

type emAddress struct {
	Street string `smorm:"length:100"`
	City   string `smorm:"length:50"`
	Geo    emGeo  `smorm:"embedded,noprefix"`
}

type emCustomer struct {
	smorm.Entity `smorm:"table:em_customer"`
	ID           int64
	Name         string    `smorm:"length:50"`
	Billing      emAddress `smorm:"embedded"`
	Shipping     emAddress `smorm:"embedded,prefix:ship_"`
}

func testORMEmbedded(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, emCustomer{})
	em := newEM()
	c := &emCustomer{Name: "c", Billing: emAddress{Street: "Main 1", City: "Berlin", Geo: emGeo{52.5, 13.4}}, Shipping: emAddress{City: "Hamburg"}}
	must(t, em.Persist(c))
	must(t, em.Persist(&emCustomer{Name: "d", Billing: emAddress{City: "Munich"}}))
	must(t, em.Flush(ctx))

	em2 := newEM()
	l, err := smorm.Find[emCustomer](ctx, em2, c.ID)
	must(t, err)
	if l.Billing != c.Billing || l.Shipping.City != "Hamburg" {
		t.Fatalf("loaded = %+v", l)
	}
	var updates []string
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "UPDATE" && strings.Contains(ev.Query, "em_customer") {
			updates = append(updates, ev.Query)
		}
	}})
	l.Shipping.Geo.Lng = 9.9
	must(t, em2.Flush(ctx))
	if len(updates) != 1 || !strings.Contains(updates[0], "ship_lng") || strings.Contains(updates[0], "billing") {
		t.Fatalf("updates = %v", updates)
	}
	found, err := smorm.NewQuery[emCustomer](newEM()).Where(smorm.Eq("Billing.City", "Berlin"), smorm.Gt("Shipping.Geo.Lng", 1)).All(ctx)
	must(t, err)
	if len(found) != 1 || found[0].Name != "c" {
		t.Fatalf("query by embedded field = %v", found)
	}
	byCriteria, err := smorm.RepositoryFor[emCustomer](newEM()).FindBy(ctx, smorm.Criteria{"Billing.City": "Munich"})
	must(t, err)
	if len(byCriteria) != 1 || byCriteria[0].Name != "d" {
		t.Fatal("criteria by embedded field")
	}
}
