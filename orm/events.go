package orm

import (
	"context"
	"slices"

	"github.com/smatify/smorm/mapping"
)

// Lifecycle hooks are methods on the entity. All of them run during Flush
// (PrePersist and PreRemove therefore see the flush context, unlike Doctrine
// where they run inside persist() and remove()). An error aborts the flush.
type (
	// PrePersister runs before a new entity is inserted.
	PrePersister interface {
		PrePersist(ctx context.Context) error
	}
	// PostPersister runs after the INSERT, inside the flush transaction.
	PostPersister interface {
		PostPersist(ctx context.Context) error
	}
	// PreUpdater runs before a changed entity is updated. It may modify the
	// entity; the change set is recomputed afterwards.
	PreUpdater interface {
		PreUpdate(ctx context.Context, cs *ChangeSet) error
	}
	// PostUpdater runs after the UPDATE, inside the flush transaction.
	PostUpdater interface {
		PostUpdate(ctx context.Context) error
	}
	// PreRemover runs before a removed entity is deleted.
	PreRemover interface {
		PreRemove(ctx context.Context) error
	}
	// PostRemover runs after the DELETE, inside the flush transaction.
	PostRemover interface {
		PostRemove(ctx context.Context) error
	}
	// PostLoader runs after an entity was hydrated from the database.
	PostLoader interface {
		PostLoad(ctx context.Context) error
	}
)

// ChangeSet describes the pending changes of an entity. Values are in their
// database representation (e.g. JSON as text).
type ChangeSet struct {
	fields []*mapping.Field
	vals   []any // old and new value of fields[i] at 2i and 2i+1
}

func newChangeSet(e *entry, changed []*mapping.Field, vals []any) *ChangeSet {
	cs := &ChangeSet{fields: changed, vals: make([]any, 2*len(changed))}
	for i, f := range changed {
		cs.vals[2*i] = e.snapshot[f.Pos]
		cs.vals[2*i+1] = vals[f.Pos]
	}
	return cs
}

// index returns the position of field in fields, or -1.
func (c *ChangeSet) index(field string) int {
	for i, f := range c.fields {
		if fieldName(f) == field {
			return i
		}
	}
	return -1
}

// fieldName is the Go field name, or the association name for join columns.
func fieldName(f *mapping.Field) string {
	if f.Assoc != nil {
		return f.Assoc.Name
	}
	return f.Name
}

// Fields returns the names of the changed fields (associations use their field name).
func (c *ChangeSet) Fields() []string {
	out := make([]string, len(c.fields))
	for i, f := range c.fields {
		out[i] = fieldName(f)
	}
	return out
}

// Has reports whether field changed.
func (c *ChangeSet) Has(field string) bool { return c.index(field) >= 0 }

// Old returns the previous database value of field.
func (c *ChangeSet) Old(field string) any {
	if i := c.index(field); i >= 0 {
		return c.vals[2*i]
	}
	return nil
}

// New returns the new database value of field.
func (c *ChangeSet) New(field string) any {
	if i := c.index(field); i >= 0 {
		return c.vals[2*i+1]
	}
	return nil
}

// Event names for EventManager listeners.
type Event string

// Events.
const (
	EventPrePersist  Event = "prePersist"
	EventPostPersist Event = "postPersist"
	EventPreUpdate   Event = "preUpdate"
	EventPostUpdate  Event = "postUpdate"
	EventPreRemove   Event = "preRemove"
	EventPostRemove  Event = "postRemove"
	EventPostLoad    Event = "postLoad"
	EventOnFlush     Event = "onFlush"   // after change sets are computed, before writing
	EventPostFlush   Event = "postFlush" // after a successful commit
	// EventOnRollback follows a flush whose writes were rolled back (also
	// by Transactional): listeners forget what they collected for args.EM.
	EventOnRollback Event = "onRollback"
)

// EventArgs is passed to listeners. Entity, Meta and ChangeSet are empty for
// onFlush, postFlush and onRollback.
type EventArgs struct {
	EM        *EntityManager
	Entity    any
	Meta      *mapping.Entity
	ChangeSet *ChangeSet
}

// Listener handles an event. Returning an error aborts the flush.
type Listener func(ctx context.Context, args *EventArgs) error

// EventManager dispatches lifecycle events to listeners. It may be shared by
// many entity managers; register listeners at startup.
type EventManager struct {
	listeners map[Event][]Listener
}

// NewEventManager returns an empty event manager.
func NewEventManager() *EventManager { return &EventManager{listeners: map[Event][]Listener{}} }

// On registers fn for the events.
func (m *EventManager) On(fn Listener, events ...Event) {
	for _, e := range events {
		m.listeners[e] = append(m.listeners[e], fn)
	}
}

// Has reports whether any listener is registered for e.
func (m *EventManager) Has(e Event) bool { return len(m.listeners[e]) > 0 }

func (m *EventManager) dispatch(ctx context.Context, e Event, args *EventArgs) error {
	for _, l := range slices.Clone(m.listeners[e]) {
		if err := l(ctx, args); err != nil {
			return err
		}
	}
	return nil
}

// WithEvents uses a shared event manager.
func WithEvents(m *EventManager) Option { return func(em *EntityManager) { em.events = m } }

// Events returns the event manager.
func (em *EntityManager) Events() *EventManager { return em.events }

// lifecycle runs the entity hook and the listeners for one entity.
func (em *EntityManager) lifecycle(ctx context.Context, ev Event, e *entry, cs *ChangeSet) error {
	var err error
	switch ev {
	case EventPrePersist:
		if h, ok := e.ptr.(PrePersister); ok {
			err = h.PrePersist(ctx)
		}
	case EventPostPersist:
		if h, ok := e.ptr.(PostPersister); ok {
			err = h.PostPersist(ctx)
		}
	case EventPreUpdate:
		if h, ok := e.ptr.(PreUpdater); ok {
			err = h.PreUpdate(ctx, cs)
		}
	case EventPostUpdate:
		if h, ok := e.ptr.(PostUpdater); ok {
			err = h.PostUpdate(ctx)
		}
	case EventPreRemove:
		if h, ok := e.ptr.(PreRemover); ok {
			err = h.PreRemove(ctx)
		}
	case EventPostRemove:
		if h, ok := e.ptr.(PostRemover); ok {
			err = h.PostRemove(ctx)
		}
	case EventPostLoad:
		if h, ok := e.ptr.(PostLoader); ok {
			err = h.PostLoad(ctx)
		}
	}
	if err != nil || !em.events.Has(ev) {
		return err
	}
	return em.events.dispatch(ctx, ev, &EventArgs{EM: em, Entity: e.ptr, Meta: e.meta, ChangeSet: cs})
}
