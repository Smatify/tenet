package smorm

import (
	"slices"
	"testing"
	"time"

	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/types"
)

type traitUser struct {
	Entity `smorm:"table:users"`
	IntID
	Timestamps
	email string `smorm:"length:180"`
}

type traitDoc struct {
	Entity
	UUIDID
	title string
}

var (
	_ mapping.CreatedToucher = (*traitUser)(nil)
	_ mapping.UpdatedToucher = (*traitUser)(nil)
)

func TestTraitMapping(t *testing.T) {
	r := mapping.NewRegistry()
	u, err := mapping.For[traitUser](r)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(u.Columns(), []string{"id", "created_at", "updated_at", "email"}) {
		t.Fatalf("columns = %v", u.Columns())
	}
	if u.Generator != mapping.GenIdentity || u.SingleID().Type.Name() != types.BigInt || !u.DoctrineStyle {
		t.Fatalf("user = %+v", u)
	}
	if f := u.Field("createdAt"); f == nil || f.Type.Name() != types.DateTime || f.Nullable {
		t.Fatalf("created_at = %+v", f)
	}
	d, err := mapping.For[traitDoc](r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Generator != mapping.GenUUIDv7 || d.SingleID().Type.Name() != types.UUID {
		t.Fatalf("doc = %+v", d)
	}
	s := mapping.BuildSchema(r.All())
	if c := s.Table("users").Column("id"); c == nil || !c.AutoIncrement {
		t.Fatal("IntID must be auto increment")
	}
}

func TestTimestampsTouch(t *testing.T) {
	var u traitUser
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	u.TouchCreated(t1)
	if !u.CreatedAt().Equal(t1) || !u.UpdatedAt().Equal(t1) {
		t.Fatal("TouchCreated")
	}
	t2 := t1.Add(time.Hour)
	u.TouchCreated(t2) // keeps the original creation time
	u.TouchUpdated(t2)
	if !u.CreatedAt().Equal(t1) || !u.UpdatedAt().Equal(t2) {
		t.Fatal("TouchUpdated")
	}
	if u.ID() != 0 {
		t.Fatal("IntID zero value")
	}
	var d traitDoc
	if !d.ID().IsZero() {
		t.Fatal("UUIDID zero value")
	}
}
