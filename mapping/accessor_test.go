package mapping

import (
	"reflect"
	"testing"
	"time"

	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/uuid"
)

func TestReflectAccessorDoctrineStyle(t *testing.T) {
	r := NewRegistry()
	e, err := For[Post](r)
	if err != nil {
		t.Fatal(err)
	}
	a := e.Accessor()
	if e.Generated {
		t.Fatal("no generated accessor registered")
	}
	ptr := a.New()
	p, ok := ptr.(*Post)
	if !ok {
		t.Fatalf("New returned %T", ptr)
	}
	id := uuid.NewV7()
	idF, titleF := e.Field("id"), e.Field("title")
	a.Set(ptr, idF.Pos, id)
	a.Set(ptr, titleF.Pos, "hello")
	if p.id != id || p.title != "hello" {
		t.Fatalf("set unexported failed: %+v", p)
	}
	if a.Get(ptr, titleF.Pos) != "hello" {
		t.Fatal("Get")
	}
	*(a.Addr(ptr, titleF.Pos).(*string)) = "via addr"
	if p.title != "via addr" {
		t.Fatal("Addr")
	}
	a.Set(ptr, titleF.Pos, nil)
	if p.title != "" {
		t.Fatal("Set nil zeroes")
	}
	u := &User{}
	author := e.Assoc("author")
	*(a.AssocAddr(ptr, author.Pos).(**User)) = u
	if p.author != u || a.GetAssoc(ptr, author.Pos).(*User) != u {
		t.Fatal("association access")
	}
	if a.Header(ptr) != &p.Header {
		t.Fatal("Header")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("join column access must panic")
		}
	}()
	a.Get(ptr, e.FieldByColumn("author_id").Pos)
}

func TestReflectAccessorEmbedded(t *testing.T) {
	r := NewRegistry()
	e, _ := For[User](r)
	a := e.Accessor()
	u := &User{}
	now := time.Now()
	a.Set(u, e.Field("CreatedAt").Pos, now)
	a.Set(u, e.Field("UpdatedAt").Pos, &now)
	if !u.CreatedAt.Equal(now) || u.UpdatedAt != &now {
		t.Fatal("embedded fields")
	}
	if e.Accessor() != a {
		t.Fatal("accessor must be cached")
	}
	for i, f := range e.Fields {
		if f.Pos != i {
			t.Fatal("Pos mismatch")
		}
	}
}

// handwritten accessor shaped like generated code
type profileAccessor struct{}

func (profileAccessor) New() any                    { return &Profile{} }
func (profileAccessor) Header(p any) *entity.Header { return &p.(*Profile).Header }
func (profileAccessor) Get(p any, i int) any        { return p.(*Profile).ID }
func (profileAccessor) Set(p any, i int, v any)     { p.(*Profile).ID = v.(int64) }
func (profileAccessor) Addr(p any, i int) any       { return &p.(*Profile).ID }
func (profileAccessor) GetAssoc(p any, i int) any   { return p.(*Profile).User }
func (profileAccessor) AssocAddr(p any, i int) any  { return &p.(*Profile).User }

func TestGeneratedAccessorWins(t *testing.T) {
	RegisterAccessor(reflect.TypeFor[Profile](), profileAccessor{})
	defer func() {
		generatedMu.Lock()
		delete(generated, reflect.TypeFor[Profile]())
		generatedMu.Unlock()
	}()
	r := NewRegistry()
	e, err := For[Profile](r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Accessor().(profileAccessor); !ok || !e.Generated {
		t.Fatal("generated accessor not used")
	}
}

func BenchmarkAccessorSet(b *testing.B) {
	r := NewRegistry()
	e, _ := For[Profile](r)
	f := e.Field("ID").Pos
	p := &Profile{}
	ra := &reflectAccessor{e: e}
	b.Run("reflect", func(b *testing.B) {
		for b.Loop() {
			ra.Set(p, f, int64(1))
		}
	})
	b.Run("generated", func(b *testing.B) {
		var ga Accessor = profileAccessor{}
		for b.Loop() {
			ga.Set(p, f, int64(1))
		}
	})
}
