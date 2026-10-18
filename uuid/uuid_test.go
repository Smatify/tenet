package uuid

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestV4(t *testing.T) {
	a, b := NewV4(), NewV4()
	if a == b || a.Version() != 4 || a[8]&0xc0 != 0x80 {
		t.Fatalf("bad v4 %s %s", a, b)
	}
	if _, ok := a.Time(); ok {
		t.Fatal("v4 has no time")
	}
}

func TestV7MonotonicAndTime(t *testing.T) {
	prev := NewV7()
	for range 10000 {
		u := NewV7()
		if bytes.Compare(prev[:], u[:]) >= 0 {
			t.Fatalf("not increasing: %s >= %s", prev, u)
		}
		if u.Version() != 7 || u[8]&0xc0 != 0x80 {
			t.Fatalf("bad version/variant %s", u)
		}
		prev = u
	}
	ts, ok := prev.Time()
	if !ok || time.Since(ts) > time.Minute || time.Until(ts) > time.Second {
		t.Fatalf("time %v %v", ts, ok)
	}
}

func TestV7FrozenClockOverflow(t *testing.T) {
	fixed := time.UnixMilli(1_700_000_000_000)
	now = func() time.Time { return fixed }
	defer func() { now = time.Now }()
	prev := NewV7()
	for range 5000 { // more than 0x0fff increments forces borrowing the next ms
		u := NewV7()
		if bytes.Compare(prev[:], u[:]) >= 0 {
			t.Fatalf("not increasing: %s >= %s", prev, u)
		}
		prev = u
	}
	ts, _ := prev.Time()
	if !ts.After(fixed) {
		t.Fatal("expected borrowed millisecond")
	}
}

func TestV7Concurrent(t *testing.T) {
	var mu sync.Mutex
	seen := map[UUID]bool{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				u := NewV7()
				mu.Lock()
				if seen[u] {
					t.Error("duplicate")
				}
				seen[u] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

func TestParseAndString(t *testing.T) {
	const s = "0190c2a8-7b1e-7c3d-9e4f-0123456789ab"
	u := MustParse(s)
	if u.String() != s {
		t.Fatalf("roundtrip %s", u)
	}
	for _, in := range []string{
		strings.ToUpper(s), "{" + s + "}", "urn:uuid:" + s, strings.ReplaceAll(s, "-", ""),
	} {
		got, err := Parse(in)
		if err != nil || got != u {
			t.Errorf("Parse(%q) = %s, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "xyz", s[:35], "0190c2a8x7b1e-7c3d-9e4f-0123456789ab", "zz90c2a8-7b1e-7c3d-9e4f-0123456789ab"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestMustParsePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	MustParse("nope")
}

func TestFromBytes(t *testing.T) {
	u := NewV4()
	got, err := FromBytes(u[:])
	if err != nil || got != u {
		t.Fatal("FromBytes")
	}
	if _, err := FromBytes([]byte{1}); err == nil {
		t.Fatal("expected error")
	}
}

func TestSQL(t *testing.T) {
	u := NewV7()
	v, err := u.Value()
	if err != nil || v != u.String() {
		t.Fatal("Value")
	}
	var got UUID
	for _, src := range []any{u.String(), []byte(u.String()), u[:], [16]byte(u)} {
		got = Nil
		if err := got.Scan(src); err != nil || got != u {
			t.Errorf("Scan(%T) = %s, %v", src, got, err)
		}
	}
	if err := got.Scan(nil); err != nil || !got.IsZero() {
		t.Error("Scan(nil)")
	}
	if err := got.Scan(42); err == nil {
		t.Error("Scan(int) should fail")
	}
	if err := got.Scan("bad"); err == nil {
		t.Error("Scan(bad) should fail")
	}
}

func TestJSON(t *testing.T) {
	type doc struct{ ID UUID }
	in := doc{NewV4()}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out doc
	if err := json.Unmarshal(b, &out); err != nil || out != in {
		t.Fatalf("json roundtrip %s %v", b, err)
	}
	if err := json.Unmarshal([]byte(`{"ID":"bad"}`), &out); err == nil {
		t.Fatal("expected error")
	}
}

func BenchmarkNewV7(b *testing.B) {
	for b.Loop() {
		_ = NewV7()
	}
}
