package dbtest

import (
	"context"
	"errors"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

func init() {
	Register(
		Case{"ORM/InheritanceSingleTable", testORMSingleTable},
		Case{"ORM/InheritanceJoined", testORMJoined},
	)
}

type inOwner struct {
	smorm.Entity `smorm:"table:in_owner"`
	ID           int64
	Name         string                      `smorm:"length:30"`
	Vehicles     smorm.Collection[inVehicle] `smorm:"oneToMany,mappedBy:Owner,orderBy:ID"`
}

type inVehicle struct {
	smorm.Entity `smorm:"table:in_vehicle,inheritance:single,discriminator:kind"`
	ID           int64
	Wheels       int
	Owner        *inOwner `smorm:"inversedBy:Vehicles"`
}

type inCar struct {
	inVehicle `smorm:"discriminatorValue:car"`
	Doors     int
}

type inSportsCar struct {
	inCar
	TopSpeed int
}

type inTruck struct {
	inVehicle `smorm:"discriminatorValue:truck"`
	Load      int
}

type inParking struct {
	smorm.Entity `smorm:"table:in_parking"`
	ID           int64
	Spot         *inVehicle `smorm:"manyToOne"`
}

func testORMSingleTable(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, inSportsCar{}, inTruck{}, inParking{})
	em := newEM()
	owner := &inOwner{Name: "olga"}
	v := &inVehicle{Wheels: 2, Owner: owner}
	car := &inCar{inVehicle: inVehicle{Wheels: 4, Owner: owner}, Doors: 5}
	sc := &inSportsCar{inCar: inCar{inVehicle: inVehicle{Wheels: 4}, Doors: 2}, TopSpeed: 300}
	truck := &inTruck{inVehicle: inVehicle{Wheels: 6, Owner: owner}, Load: 40}
	for _, x := range []any{owner, v, car, sc, truck} {
		must(t, em.Persist(x))
	}
	must(t, em.Flush(ctx))
	park := &inParking{Spot: &truck.inVehicle}
	must(t, em.Persist(park))
	must(t, em.Flush(ctx))
	if Count(t, db, "in_vehicle") != 4 {
		t.Fatal("single table")
	}

	em2 := newEM()
	lc, err := smorm.Find[inCar](ctx, em2, car.ID)
	must(t, err)
	if lc.Doors != 5 || lc.Wheels != 4 {
		t.Fatalf("car = %+v", lc)
	}
	view, err := smorm.Find[inVehicle](ctx, em2, car.ID)
	must(t, err)
	if view != &lc.inVehicle || smorm.Concrete(view) != any(lc) {
		t.Fatal("root lookup must return a view into the same instance")
	}
	if _, err := smorm.Find[inTruck](ctx, em2, car.ID); !errors.Is(err, smorm.ErrNotFound) {
		t.Fatalf("wrong class = %v", err)
	}
	em3 := newEM()
	poly, err := smorm.Find[inVehicle](ctx, em3, sc.ID)
	must(t, err)
	if s, ok := smorm.Concrete(poly).(*inSportsCar); !ok || s.TopSpeed != 300 || s.Doors != 2 {
		t.Fatalf("polymorphic find = %T", smorm.Concrete(poly))
	}
	all, err := smorm.NewQuery[inVehicle](em3).OrderBy("ID", smorm.Asc).All(ctx)
	must(t, err)
	kinds := ""
	for _, x := range all {
		switch smorm.Concrete(x).(type) {
		case *inSportsCar:
			kinds += "S"
		case *inCar:
			kinds += "C"
		case *inTruck:
			kinds += "T"
		case *inVehicle:
			kinds += "V"
		}
	}
	if kinds != "VCST" {
		t.Fatalf("polymorphic query = %s", kinds)
	}
	cars, err := smorm.NewQuery[inCar](em3).All(ctx)
	must(t, err)
	if len(cars) != 2 {
		t.Fatalf("cars incl. sports cars = %d", len(cars))
	}
	if n, _ := smorm.NewQuery[inCar](em3).Count(ctx); n != 2 {
		t.Fatalf("count cars = %d", n)
	}
	heavy, err := smorm.NewQuery[inTruck](em3).Where(smorm.Gt("Load", 10)).All(ctx)
	must(t, err)
	if len(heavy) != 1 {
		t.Fatal("subclass field query")
	}
	// Polymorphic collection and to-one.
	o, err := smorm.Find[inOwner](ctx, newEM(), owner.ID)
	must(t, err)
	if vs := o.Vehicles.All(); len(vs) != 3 {
		t.Fatalf("collection = %d", len(vs))
	} else if _, ok := smorm.Concrete(vs[2]).(*inTruck); !ok {
		t.Fatalf("collection item = %T", smorm.Concrete(vs[2]))
	}
	p, err := smorm.Find[inParking](ctx, newEM(), park.ID)
	must(t, err)
	if tr, ok := smorm.Concrete(p.Spot).(*inTruck); !ok || tr.Load != 40 {
		t.Fatalf("polymorphic to-one = %T", smorm.Concrete(p.Spot))
	}
	// Updates and deletes.
	em4 := newEM()
	c4, _ := smorm.Find[inCar](ctx, em4, car.ID)
	c4.Doors = 3
	c4.Wheels = 3
	must(t, em4.Flush(ctx))
	c5, _ := smorm.Find[inCar](ctx, newEM(), car.ID)
	if c5.Doors != 3 || c5.Wheels != 3 {
		t.Fatal("update")
	}
	em5 := newEM()
	p5, _ := smorm.Find[inParking](ctx, em5, park.ID)
	must(t, em5.Remove(p5))
	tr5, _ := smorm.Find[inTruck](ctx, em5, truck.ID)
	must(t, em5.Remove(tr5))
	must(t, em5.Flush(ctx))
	if Count(t, db, "in_vehicle") != 3 {
		t.Fatal("delete")
	}
	if _, err := smorm.Reference[inVehicle](newEM(), car.ID); err == nil {
		t.Fatal("references to polymorphic classes are not possible")
	}
}

type inPayment struct {
	smorm.Entity `smorm:"table:in_payment,inheritance:joined,abstract"`
	ID           int64
	Amount       int64
}

type inCardPayment struct {
	inPayment `smorm:"table:in_card_payment,discriminatorValue:card"`
	Last4     string `smorm:"length:4"`
}

type inWirePayment struct {
	inPayment `smorm:"table:in_wire_payment,discriminatorValue:wire"`
	IBAN      string `smorm:"length:34"`
}

func testORMJoined(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, inCardPayment{}, inWirePayment{})
	em := newEM()
	card := &inCardPayment{inPayment: inPayment{Amount: 100}, Last4: "4242"}
	wire := &inWirePayment{inPayment: inPayment{Amount: 250}, IBAN: "DE00"}
	must(t, em.Persist(card))
	must(t, em.Persist(wire))
	must(t, em.Flush(ctx))
	if card.ID == 0 || Count(t, db, "in_payment") != 2 || Count(t, db, "in_card_payment") != 1 || Count(t, db, "in_wire_payment") != 1 {
		t.Fatal("joined insert")
	}
	em2 := newEM()
	lc, err := smorm.Find[inCardPayment](ctx, em2, card.ID)
	must(t, err)
	if lc.Amount != 100 || lc.Last4 != "4242" {
		t.Fatalf("card = %+v", lc)
	}
	all, err := smorm.NewQuery[inPayment](newEM()).OrderBy("Amount", smorm.Desc).All(ctx)
	must(t, err)
	if len(all) != 2 {
		t.Fatal("polymorphic joined query")
	}
	if w, ok := smorm.Concrete(all[0]).(*inWirePayment); !ok || w.IBAN != "DE00" {
		t.Fatalf("first = %T", smorm.Concrete(all[0]))
	}
	found, err := smorm.NewQuery[inCardPayment](newEM()).Where(smorm.Eq("Last4", "4242"), smorm.Gte("Amount", 50)).All(ctx)
	must(t, err)
	if len(found) != 1 {
		t.Fatal("query on joined subclass")
	}
	lc.Amount = 120
	lc.Last4 = "1111"
	must(t, em2.Flush(ctx))
	again, _ := smorm.Find[inCardPayment](ctx, newEM(), card.ID)
	if again.Amount != 120 || again.Last4 != "1111" {
		t.Fatalf("joined update = %+v", again)
	}
	em3 := newEM()
	rc, _ := smorm.Find[inCardPayment](ctx, em3, card.ID)
	must(t, em3.Remove(rc))
	must(t, em3.Flush(ctx))
	if Count(t, db, "in_payment") != 1 || Count(t, db, "in_card_payment") != 0 {
		t.Fatal("joined delete must cascade")
	}
}
