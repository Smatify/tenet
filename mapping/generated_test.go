package mapping

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/smatify/tenet/internal/entity"
)

func TestComposedAccessor(t *testing.T) {
	typ := reflect.TypeFor[User]()
	calls := 0
	RegisterGenerated(typ, GeneratedType{
		New:    func() any { calls++; return &User{} },
		Header: func(p any) *entity.Header { calls++; return &p.(*User).Header },
		Fields: map[string]GeneratedField{
			"Email": {
				Get:  func(p any) any { calls++; return p.(*User).Email },
				Set:  func(p any, v any) { calls++; p.(*User).Email = v.(string) },
				Addr: func(p any) any { calls++; return &p.(*User).Email },
			},
		},
		Assocs: map[string]GeneratedField{
			"Profile": {
				Get:  func(p any) any { calls++; return p.(*User).Profile },
				Addr: func(p any) any { calls++; return &p.(*User).Profile },
			},
		},
	})
	defer func() {
		generatedMu.Lock()
		delete(generatedTypes, typ)
		generatedMu.Unlock()
	}()
	e, err := For[User](NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	a := e.Accessor()
	if !e.Generated {
		t.Fatal("Generated flag")
	}
	u := a.New().(*User)
	_ = a.Header(u)
	email := e.Field("Email").Pos
	a.Set(u, email, "x@y")
	_ = a.Get(u, email)
	_ = a.Addr(u, email)
	prof := e.Assoc("Profile").Pos
	_ = a.GetAssoc(u, prof)
	_ = a.AssocAddr(u, prof)
	if calls != 7 {
		t.Fatalf("generated funcs called %d times", calls)
	}
	// Fields without generated funcs use reflection.
	now := time.Now()
	a.Set(u, e.Field("CreatedAt").Pos, now)
	if !a.Get(u, e.Field("CreatedAt").Pos).(time.Time).Equal(now) || a.Addr(u, e.Field("CreatedAt").Pos) == nil {
		t.Fatal("reflection fallback")
	}
	posts := e.Assoc("Posts").Pos
	if a.AssocAddr(u, posts) == nil || a.GetAssoc(u, posts) == nil {
		t.Fatal("assoc fallback")
	}
	if path(typ, e.Field("UpdatedAt").Index) != "Timestamps.UpdatedAt" {
		t.Fatal("path")
	}
}

func TestComposedAccessorEmptyGenerated(t *testing.T) {
	typ := reflect.TypeFor[Group]()
	RegisterGenerated(typ, GeneratedType{})
	defer func() {
		generatedMu.Lock()
		delete(generatedTypes, typ)
		generatedMu.Unlock()
	}()
	e, err := For[Group](NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	a := e.Accessor()
	g := a.New().(*Group)
	if a.Header(g) != &g.Header {
		t.Fatal("header fallback")
	}
}

func TestStaleGenerated(t *testing.T) {
	typ := reflect.TypeFor[Profile]()
	e, err := For[Profile](NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if e.StaleGenerated() != nil {
		t.Fatal("nothing registered")
	}
	RegisterGenerated(typ, GeneratedType{
		Fields: map[string]GeneratedField{"ID": {}, "Gone": {}},
	})
	defer func() {
		generatedMu.Lock()
		delete(generatedTypes, typ)
		generatedMu.Unlock()
	}()
	got := e.StaleGenerated()
	want := []string{"Profile: association User is not generated", "Profile: generated field Gone is no longer mapped"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

type foreignEmbed struct {
	entity.Header
	ID        int64
	time.Time `tenet:"column:ts"`
}

func TestAccessible(t *testing.T) {
	typ := reflect.TypeFor[Post]()
	e, _ := For[Post](NewRegistry())
	if !accessible(typ, e.Field("title").Index) {
		t.Fatal("same package unexported is accessible")
	}
	ft := reflect.TypeFor[foreignEmbed]()
	// time.Time's unexported "wall" field is not accessible from this package.
	if accessible(ft, []int{2, 0}) || !accessible(ft, []int{2}) {
		t.Fatal("foreign unexported field")
	}
}
