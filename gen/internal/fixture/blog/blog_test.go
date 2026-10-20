package blog

import (
	"testing"

	"github.com/smatify/smorm/mapping"
)

// These tests exercise the committed generated file.
func TestGeneratedAccessors(t *testing.T) {
	r := mapping.NewRegistry()
	p, err := mapping.For[Post](r)
	if err != nil {
		t.Fatal(err)
	}
	a := p.Accessor()
	if !p.Generated {
		t.Fatal("generated accessor not registered")
	}
	post := a.New().(*Post)
	a.Set(post, p.Field("title").Pos, "hello")
	a.Set(post, p.Field("meta").Pos, Meta{Tags: []string{"go"}})
	if post.title != "hello" || post.meta.Tags[0] != "go" || a.Get(post, p.Field("title").Pos) != "hello" {
		t.Fatal("generated field access")
	}
	a.Set(post, p.Field("title").Pos, nil)
	if post.title != "" || a.Addr(post, p.Field("title").Pos).(*string) != &post.title {
		t.Fatal("zeroing / Addr")
	}
	// Trait fields live in another package and use the reflection fallback.
	if a.Get(post, p.Field("id").Pos) == nil {
		t.Fatal("fallback")
	}
	au := &Author{}
	*(a.AssocAddr(post, p.Assoc("author").Pos).(**Author)) = au
	if post.author != au || a.GetAssoc(post, p.Assoc("author").Pos) != au || a.Header(post) != &post.Entity {
		t.Fatal("assoc / header")
	}
	if PostFields.Title.Name != "title" || AuthorFields.Email.Entity != "Author" || AuthorFields.AddressCity.Name != "Address.City" {
		t.Fatal("field refs")
	}
	ae, err := mapping.For[Author](r)
	if err != nil {
		t.Fatal(err)
	}
	if !ae.Accessor().(interface{ New() any }).New().(*Author).Nick.Valid == false {
		t.Fatal("new author")
	}
}

func TestGeneratedEmbeddable(t *testing.T) {
	r := mapping.NewRegistry()
	e, err := mapping.For[Author](r)
	if err != nil {
		t.Fatal(err)
	}
	f := e.Field("Address.City")
	if f == nil || f.Column != "addr_city" {
		t.Fatalf("field = %+v", f)
	}
	a := &Author{}
	e.Accessor().Set(a, f.Pos, "Berlin")
	if a.Address.City != "Berlin" || len(e.StaleGenerated()) != 0 {
		t.Fatalf("generated embeddable access: %+v %v", a.Address, e.StaleGenerated())
	}
}

func TestGeneratedSubclass(t *testing.T) {
	r := mapping.NewRegistry()
	v, err := mapping.For[Video](r)
	if err != nil {
		t.Fatal(err)
	}
	vid := &Video{}
	v.Accessor().Set(vid, v.Field("Title").Pos, "intro")
	v.Accessor().Set(vid, v.Field("Seconds").Pos, 90)
	if vid.Title != "intro" || vid.Seconds != 90 || !v.Generated || len(v.StaleGenerated()) != 0 {
		t.Fatalf("subclass accessors: %+v %v", vid, v.StaleGenerated())
	}
	if v.Accessor().Header(vid) != &vid.Entity {
		t.Fatal("header")
	}
	if VideoFields.Seconds.Name != "Seconds" {
		t.Fatal("refs")
	}
}
