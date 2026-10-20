package orm

import (
	"testing"
	"time"
)

func TestAssign(t *testing.T) {
	var i int
	var i32 int32
	var s string
	var b bool
	var ps *string
	var bs []byte
	var tm time.Time
	ok := []struct {
		dest, src any
	}{
		{&i, int64(5)}, {&i32, int64(7)}, {&s, "x"}, {&s, []byte("y")}, {&b, int64(1)}, {&ps, "p"}, {&bs, "z"},
		{&tm, "2026-01-02T03:04:05Z"}, {&tm, time.Unix(1, 0)},
	}
	for _, c := range ok {
		if err := assign(c.dest, c.src); err != nil {
			t.Errorf("assign(%T, %T): %v", c.dest, c.src, err)
		}
	}
	if i != 5 || i32 != 7 || s != "y" || !b || *ps != "p" || string(bs) != "z" || tm.Unix() != 1 {
		t.Fatalf("values: %v %v %q %v %v %q %v", i, i32, s, b, *ps, bs, tm)
	}
	if err := assign(&ps, nil); err != nil || ps != nil {
		t.Fatal("nil")
	}
	for _, c := range []struct{ dest, src any }{{&i, "x"}, {i, 1}, {&tm, "bad"}, {&ps, struct{}{}}} {
		if err := assign(c.dest, c.src); err == nil {
			t.Errorf("assign(%T, %T) must fail", c.dest, c.src)
		}
	}
}

func TestAssignText(t *testing.T) {
	var i int64
	var u uint8
	var f float64
	var b bool
	var tm time.Time
	for _, c := range []struct{ dest, src any }{{&i, []byte("42")}, {&u, []byte("7")}, {&f, []byte("1.5")}, {&b, []byte("1")}, {&tm, []byte("2026-01-02T03:04:05Z")}} {
		if err := assign(c.dest, c.src); err != nil {
			t.Fatalf("assign(%T): %v", c.dest, err)
		}
	}
	if i != 42 || u != 7 || f != 1.5 || !b || tm.Year() != 2026 {
		t.Fatal("values")
	}
	for _, d := range []any{&i, &u, &f, &b} {
		if err := assign(d, []byte("x")); err == nil {
			t.Errorf("%T: expected parse error", d)
		}
	}
	if normalizeText([]byte("a")) != "a" || normalizeText(1) != 1 {
		t.Fatal("normalizeText")
	}
}
