// Package entity holds the header embedded in every entity. It lives in an
// internal package so only smorm can touch its state; the root package
// re-exports it as smorm.Entity.
package entity

// Header marks a struct as an entity and carries per-instance ORM state
// (managed/new/removed, proxy initialization, owning entity manager).
// The zero value is a new, unmanaged entity.
type Header struct {
	state any
}

// entityHeader is promoted to every struct embedding Header, which lets
// Of find the header without reflection.
func (h *Header) entityHeader() *Header { return h }

type carrier interface{ entityHeader() *Header }

// Of returns the header of an entity pointer, or nil if p is not one.
func Of(p any) *Header {
	if c, ok := p.(carrier); ok {
		return c.entityHeader()
	}
	return nil
}

// State returns the opaque ORM state.
func State(h *Header) any { return h.state }

// SetState replaces the opaque ORM state.
func SetState(h *Header, s any) { h.state = s }

// Loader is implemented by ORM state that can load an uninitialized entity.
type Loader interface{ EnsureLoaded() }

// Touch loads p if it is an uninitialized reference. Getters of lazily
// loaded entities call it before reading fields.
func Touch(p any) {
	if c, ok := p.(carrier); ok {
		if l, ok := c.entityHeader().state.(Loader); ok {
			l.EnsureLoaded()
		}
	}
}
