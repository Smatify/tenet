// Package cache provides the second-level cache for entity data. Entries
// hold the database values of an entity's columns and are shared by all
// entity managers using the same cache.
package cache

import (
	"container/list"
	"sync"
	"time"
)

// Cache stores entity data. Implementations must be safe for concurrent use;
// values must be treated as immutable.
type Cache interface {
	Get(key string) ([]any, bool)
	Set(key string, values []any, ttl time.Duration)
	Delete(key string)
	Clear()
}

// Stats counts cache activity.
type Stats struct {
	Hits, Misses, Puts, Evictions int64
}

// Memory is an in-process LRU cache with optional per-entry TTL.
type Memory struct {
	mu    sync.Mutex
	max   int
	ll    *list.List
	items map[string]*list.Element
	now   func() time.Time
	stats Stats
}

type item struct {
	key     string
	values  []any
	expires time.Time // zero: never
}

// NewMemory returns an LRU cache holding at most max entries (0 = 10000).
func NewMemory(max int) *Memory {
	if max <= 0 {
		max = 10000
	}
	return &Memory{max: max, ll: list.New(), items: map[string]*list.Element{}, now: time.Now}
}

// Get implements Cache.
func (m *Memory) Get(key string) ([]any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	el, ok := m.items[key]
	if !ok {
		m.stats.Misses++
		return nil, false
	}
	it := el.Value.(*item)
	if !it.expires.IsZero() && m.now().After(it.expires) {
		m.ll.Remove(el)
		delete(m.items, key)
		m.stats.Misses++
		return nil, false
	}
	m.ll.MoveToFront(el)
	m.stats.Hits++
	return it.values, true
}

// Set implements Cache. ttl <= 0 means no expiry.
func (m *Memory) Set(key string, values []any, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.Puts++
	it := &item{key: key, values: values}
	if ttl > 0 {
		it.expires = m.now().Add(ttl)
	}
	if el, ok := m.items[key]; ok {
		el.Value = it
		m.ll.MoveToFront(el)
		return
	}
	m.items[key] = m.ll.PushFront(it)
	for m.ll.Len() > m.max {
		last := m.ll.Back()
		m.ll.Remove(last)
		delete(m.items, last.Value.(*item).key)
		m.stats.Evictions++
	}
}

// Delete implements Cache.
func (m *Memory) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.items[key]; ok {
		m.ll.Remove(el)
		delete(m.items, key)
	}
}

// Clear implements Cache.
func (m *Memory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ll.Init()
	m.items = map[string]*list.Element{}
}

// Len returns the number of entries.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ll.Len()
}

// Stats returns a snapshot of the counters.
func (m *Memory) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}
