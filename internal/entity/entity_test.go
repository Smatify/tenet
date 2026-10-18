package entity

import "testing"

type user struct {
	Header
	Name string
}

type loader struct{ n *int }

func (l loader) EnsureLoaded() { *l.n++ }

func TestHeader(t *testing.T) {
	u := &user{}
	h := Of(u)
	if h != &u.Header || Of(42) != nil || State(h) != nil {
		t.Fatal("Of")
	}
	n := 0
	SetState(h, loader{&n})
	Touch(u)
	Touch("not an entity")
	if n != 1 {
		t.Fatal("Touch")
	}
	SetState(h, "plain")
	Touch(u)
	if n != 1 || State(h) != "plain" {
		t.Fatal("non loader state")
	}
}
