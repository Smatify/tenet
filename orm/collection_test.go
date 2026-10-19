package orm

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

type citem struct{ n int }

func TestPlainCollection(t *testing.T) {
	a, b, c := &citem{1}, &citem{2}, &citem{3}
	col := NewCollection(a, b)
	if !col.IsInitialized() || col.Len() != 2 || col.Get(1) != b || !col.Contains(a) || col.Contains(c) {
		t.Fatal("basics")
	}
	col.Add(c, a) // a is already present
	if !slices.Equal(col.All(), []*citem{a, b, c}) {
		t.Fatalf("All = %v", col.All())
	}
	var seen []*citem
	for it := range col.Iter() {
		seen = append(seen, it)
	}
	if len(seen) != 3 || !col.Remove(b) || col.Remove(b) || len(col.Entities()) != 2 {
		t.Fatal("iter/remove")
	}
	col.Clear()
	if col.Len() != 0 || col.CollectionElem() != reflect.TypeFor[citem]() {
		t.Fatal("clear")
	}
	if err := col.load(context.Background()); err != nil {
		t.Fatal("loading an initialized collection is a no-op")
	}
	var zero Collection[citem]
	zero.Add(a)
	if zero.Len() != 1 || zero.state() != nil {
		t.Fatal("zero value collection")
	}
}

func TestRef(t *testing.T) {
	var r Ref[citem]
	if !r.IsNil() || r.Get() != nil || r.Ptr() != nil || r.refPtr() != nil || r.RefElem() != reflect.TypeFor[citem]() {
		t.Fatal("empty ref")
	}
	if err := r.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	x := &citem{7}
	r.Set(x)
	if r.Get() != x || r.refPtr() != any(x) {
		t.Fatal("set")
	}
	r.setRefPtr(nil)
	if !r.IsNil() {
		t.Fatal("setRefPtr nil")
	}
	n := NewRef(x)
	if n.Ptr() != x {
		t.Fatal("NewRef")
	}
	var em *EntityManager
	if em.lazyContext() == nil {
		t.Fatal("nil manager context")
	}
}
