package mapping

import (
	"slices"
	"strings"
	"testing"

	"github.com/smatify/tenet/internal/entity"
	"github.com/smatify/tenet/schema"
)

type vehicle struct {
	entity.Header `tenet:"table:vehicles,inheritance:single,discriminator:kind"`
	ID            int64
	Wheels        int
	Owner         *User `tenet:"manyToOne"`
}

type car struct {
	vehicle `tenet:"discriminatorValue:car"`
	Doors   int
}

type sportsCar struct {
	car
	TopSpeed int
}

type truck struct {
	vehicle
	Load  int
	Fleet *Group `tenet:"manyToOne"`
}

type payment struct {
	entity.Header `tenet:"inheritance:joined,abstract"`
	ID            int64
	Amount        int64
}

type cardPayment struct {
	payment `tenet:"table:card_payments"`
	Last4   string `tenet:"length:4,index"`
}

func TestSingleTableMapping(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(car{}, sportsCar{}, truck{}); err != nil {
		t.Fatal(err)
	}
	v, _ := For[vehicle](r)
	c, _ := For[car](r)
	sc, _ := For[sportsCar](r)
	tr, _ := For[truck](r)
	if v.Inheritance != SingleTable || v.Root != v || v.DiscriminatorColumn != "kind" || v.DiscriminatorValue != "vehicle" {
		t.Fatalf("root = %+v", v)
	}
	if c.Parent != v || c.Root != v || c.Table != "vehicles" || c.DiscriminatorValue != "car" || sc.DiscriminatorValue != "sports_car" || sc.Parent != c {
		t.Fatalf("car = %+v", c)
	}
	if !slices.Equal(c.Columns(), []string{"id", "wheels", "owner_id", "doors"}) || len(c.ID) != 1 || c.Generator != GenIdentity {
		t.Fatalf("car columns = %v", c.Columns())
	}
	if f := sc.Field("Wheels"); f == nil || len(f.Index) != 3 || f.Table != "vehicles" {
		t.Fatalf("inherited field = %+v", f)
	}
	if f := c.Field("Doors"); !f.Nullable {
		t.Fatal("single table subclass columns are nullable")
	}
	if a := c.Assoc("Owner"); a == nil || !a.Inherited || a.Source != c || c.FieldByColumn("owner_id").Assoc != a {
		t.Fatal("inherited association")
	}
	if len(v.Subclasses) != 2 || len(v.Descendants()) != 4 || v.ClassForDiscriminator("sports_car") != sc || v.ClassForDiscriminator("x") != nil || !v.IsPolymorphic() || tr.IsPolymorphic() {
		t.Fatal("hierarchy navigation")
	}
	if !slices.Equal(sc.Tables(), []string{"vehicles"}) {
		t.Fatal("Tables")
	}
	s := BuildSchema(r.All())
	vt := s.Table("vehicles")
	if vt == nil || vt.Column("kind") == nil || vt.Column("doors") == nil || vt.Column("top_speed") == nil || vt.Column("load") == nil {
		t.Fatalf("single table = %+v", vt)
	}
	if !vt.Column("load").Nullable || vt.Column("kind").Nullable || vt.ForeignKey("fk_vehicles_fleet_id") == nil || vt.ForeignKey("fk_vehicles_owner_id") == nil {
		t.Fatal("subclass columns and constraints")
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestJoinedMapping(t *testing.T) {
	r := NewRegistry()
	cp, err := For[cardPayment](r)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := For[payment](r)
	if !p.Abstract || p.Inheritance != Joined || cp.Table != "card_payments" || p.Table != "payment" {
		t.Fatalf("payment = %+v", p)
	}
	if !slices.Equal(cp.Tables(), []string{"payment", "card_payments"}) || cp.Field("Amount").Table != "payment" || cp.Field("Last4").Table != "card_payments" {
		t.Fatal("tables")
	}
	s := BuildSchema([]*Entity{cp})
	ct := s.Table("card_payments")
	if ct == nil || !slices.Equal(ct.PrimaryKey.Columns, []string{"id"}) || ct.Column("id").AutoIncrement || ct.Column("amount") != nil {
		t.Fatalf("child table = %+v", ct)
	}
	if fk := ct.ForeignKeys[0]; fk.RefTable != "payment" || fk.OnDelete != schema.Cascade || ct.Index("idx_card_payments_last4") == nil {
		t.Fatal("child FK / index")
	}
	if s.Table("payment").Column("dtype") == nil {
		t.Fatal("discriminator")
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

type noInheritance struct {
	entity.Header
	ID int64
}
type badChild struct {
	noInheritance
}
type stiTable struct {
	vehicle `tenet:"table:x"`
}
type dupDiscriminator struct {
	vehicle `tenet:"discriminatorValue:car"`
}
type childID struct {
	vehicle `tenet:"discriminatorValue:cid"`
	Other   int64 `tenet:"id"`
}
type badSubOpt struct {
	vehicle `tenet:"inheritance:joined"`
}
type badInheritance struct {
	entity.Header `tenet:"inheritance:diamond"`
	ID            int64
}
type discNoInheritance struct {
	entity.Header `tenet:"discriminator:x"`
	ID            int64
}
type discCollision struct {
	entity.Header `tenet:"inheritance:single,discriminator:name"`
	ID            int64
	Name          string
}

func TestInheritanceErrors(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{badChild{}, "does not declare inheritance"}, {stiTable{}, "remove table"}, {childID{}, "inherit the identifier"},
		{badSubOpt{}, "unknown subclass tag option"}, {badInheritance{}, "unknown inheritance"},
		{discNoInheritance{}, "need inheritance"}, {discCollision{}, "collides"},
	}
	for _, c := range cases {
		r := NewRegistry()
		if err := r.Register(c.v); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%T: %v", c.v, err)
		}
	}
	r := NewRegistry()
	_ = r.Register(car{})
	if err := r.Register(dupDiscriminator{}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("duplicate discriminator: %v", err)
	}
}

type cachedEntity struct {
	entity.Header `tenet:"cache,cacheTTL:90s"`
	ID            int64
}
type badTTL struct {
	entity.Header `tenet:"cacheTTL:soon"`
	ID            int64
}

func TestCacheOptions(t *testing.T) {
	e, err := For[cachedEntity](NewRegistry())
	if err != nil || !e.Cacheable || e.CacheTTL.Seconds() != 90 {
		t.Fatalf("cache options = %+v %v", e, err)
	}
	if err := NewRegistry().Register(badTTL{}); err == nil {
		t.Fatal("invalid ttl")
	}
}
