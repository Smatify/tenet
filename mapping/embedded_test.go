package mapping

import (
	"slices"
	"strings"
	"testing"

	"github.com/smatify/smorm/internal/entity"
)

type geo struct {
	Lat, Lng float64
}

type address struct {
	Street string `smorm:"length:100,index"`
	City   string `smorm:"column:town"`
	Geo    geo    `smorm:"embedded,noprefix"`
}

type customer struct {
	entity.Header
	ID       int64
	Billing  address  `smorm:"embedded"`
	Shipping address  `smorm:"embedded,prefix:ship_"`
	Note     *address `smorm:"-"`
}

func TestEmbeddables(t *testing.T) {
	e, err := For[customer](NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "billing_street", "billing_town", "billing_lat", "billing_lng", "ship_street", "ship_town", "ship_lat", "ship_lng"}
	if !slices.Equal(e.Columns(), want) {
		t.Fatalf("columns = %v", e.Columns())
	}
	if f := e.Field("Shipping.Geo.Lng"); f == nil || f.Column != "ship_lng" || len(f.Index) != 3 {
		t.Fatalf("nested field = %+v", f)
	}
	if e.Indexes[0].Columns[0] != "billing_street" || e.Indexes[0].Name != "idx_customer_billing_street" {
		t.Fatalf("index = %+v", e.Indexes[0])
	}
	c := &customer{}
	e.Accessor().Set(c, e.Field("Billing.Geo.Lat").Pos, 52.5)
	if c.Billing.Geo.Lat != 52.5 {
		t.Fatal("nested accessor")
	}
}

type embedPointer struct {
	entity.Header
	ID int64
	A  *address `smorm:"embedded"`
}
type embedEntity struct {
	entity.Header
	ID int64
	U  User `smorm:"embedded"`
}
type prefixWithoutEmbed struct {
	entity.Header
	ID int64
	S  string `smorm:"prefix:x_"`
}
type assocInEmbed struct {
	entity.Header
	ID int64
	W  withAssoc `smorm:"embedded"`
}
type withAssoc struct{ U *User }
type idInEmbed struct {
	entity.Header
	ID int64
	W  withID `smorm:"embedded"`
}
type withID struct {
	X int64 `smorm:"id"`
}

func TestEmbeddableErrors(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{embedPointer{}, "struct values"}, {embedEntity{}, "struct values"}, {prefixWithoutEmbed{}, "need embedded"},
		{assocInEmbed{}, "associations inside embeddables"}, {idInEmbed{}, "identifiers inside embeddables"},
	} {
		v, want := c.v, c.want
		if err := NewRegistry().Register(v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%T: %v", v, err)
		}
	}
}
