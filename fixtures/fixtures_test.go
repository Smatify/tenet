package fixtures

import (
	"context"
	"strings"
	"testing"
)

func noop(context.Context, *Context) error { return nil }

func names(fs []Fixture) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return strings.Join(out, ",")
}

func TestOrdering(t *testing.T) {
	r := NewRegistry()
	for _, f := range []Fixture{
		{Name: "posts", DependsOn: []string{"users", "tags"}, Groups: []string{"blog"}, Load: noop},
		{Name: "users", Load: noop},
		{Name: "tags", Load: noop, Groups: []string{"blog"}},
		{Name: "admin", Load: noop, Groups: []string{"admin"}},
	} {
		if err := r.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	all, err := r.Ordered()
	if err != nil || names(all) != "admin,tags,users,posts" {
		t.Fatalf("all = %s %v", names(all), err)
	}
	blog, _ := r.Ordered("blog")
	if names(blog) != "tags,users,posts" {
		t.Fatalf("blog = %s", names(blog))
	}
	if err := r.Add(Fixture{Name: "users", Load: noop}); err == nil {
		t.Fatal("duplicate")
	}
	if err := r.Add(Fixture{Name: "x"}); err == nil {
		t.Fatal("missing Load")
	}
	_ = r.Add(Fixture{Name: "broken", DependsOn: []string{"nope"}, Load: noop})
	if _, err := r.Ordered(); err == nil || !strings.Contains(err.Error(), `unknown fixture "nope"`) {
		t.Fatalf("unknown dep: %v", err)
	}
	c := NewRegistry()
	_ = c.Add(Fixture{Name: "a", DependsOn: []string{"b"}, Load: noop})
	_ = c.Add(Fixture{Name: "b", DependsOn: []string{"a"}, Load: noop})
	if _, err := c.Ordered(); err == nil || !strings.Contains(err.Error(), "cycle a -> b -> a") {
		t.Fatalf("cycle: %v", err)
	}
}

func TestReferences(t *testing.T) {
	type user struct{ name string }
	c := &Context{refs: map[string]any{}}
	u := &user{"ann"}
	c.AddReference("ann", u)
	if !c.HasReference("ann") || c.Reference("ann") != u || Ref[user](c, "ann") != u || c.HasReference("bob") {
		t.Fatal("references")
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "missing") {
			t.Fatalf("panic = %v", r)
		}
	}()
	Ref[user](c, "bob")
}

func TestRegisterPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	Register(Fixture{})
}
