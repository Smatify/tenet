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

// State returns the opaque ORM state.
func (h *Header) State() any { return h.state }

// SetState replaces the opaque ORM state.
func (h *Header) SetState(s any) { h.state = s }
