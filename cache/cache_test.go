package cache

import (
	"sync"
	"testing"
	"time"
)

func TestMemoryLRUAndTTL(t *testing.T) {
	m := NewMemory(2)
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.Set("a", []any{1}, 0)
	m.Set("b", []any{2}, time.Second)
	if v, ok := m.Get("a"); !ok || v[0] != 1 {
		t.Fatal("get a")
	}
	m.Set("c", []any{3}, 0) // evicts b (least recently used)
	if _, ok := m.Get("b"); ok {
		t.Fatal("b must be evicted")
	}
	m.Set("a", []any{10}, time.Second)
	now = now.Add(2 * time.Second)
	if _, ok := m.Get("a"); ok {
		t.Fatal("a must be expired")
	}
	if v, ok := m.Get("c"); !ok || v[0] != 3 {
		t.Fatal("c")
	}
	m.Delete("c")
	m.Delete("missing")
	if m.Len() != 0 {
		t.Fatalf("len = %d", m.Len())
	}
	s := m.Stats()
	if s.Hits != 2 || s.Misses != 2 || s.Puts != 4 || s.Evictions != 1 {
		t.Fatalf("stats = %+v", s)
	}
	m.Set("x", nil, 0)
	m.Clear()
	if m.Len() != 0 || NewMemory(0).max != 10000 {
		t.Fatal("clear/default")
	}
}

func TestMemoryConcurrent(t *testing.T) {
	m := NewMemory(100)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 200 {
				k := string(rune('a' + (i+j)%26))
				m.Set(k, []any{j}, 0)
				m.Get(k)
			}
		}()
	}
	wg.Wait()
	var _ Cache = m
}
