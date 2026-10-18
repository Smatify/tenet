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
	if PostFields.Title.Name != "title" || AuthorFields.Email.Entity != "Author" {
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
