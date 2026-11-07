package tenet

import "github.com/smatify/tenet/orm"

// Lifecycle hook interfaces implemented by entities.
type (
	PrePersister  = orm.PrePersister
	PostPersister = orm.PostPersister
	PreUpdater    = orm.PreUpdater
	PostUpdater   = orm.PostUpdater
	PreRemover    = orm.PreRemover
	PostRemover   = orm.PostRemover
	PostLoader    = orm.PostLoader
)

// ChangeSet describes pending changes passed to PreUpdate.
type ChangeSet = orm.ChangeSet

// Event manager types.
type (
	EventManager = orm.EventManager
	Event        = orm.Event
	EventArgs    = orm.EventArgs
	Listener     = orm.Listener
)

// Events.
const (
	EventPrePersist  = orm.EventPrePersist
	EventPostPersist = orm.EventPostPersist
	EventPreUpdate   = orm.EventPreUpdate
	EventPostUpdate  = orm.EventPostUpdate
	EventPreRemove   = orm.EventPreRemove
	EventPostRemove  = orm.EventPostRemove
	EventPostLoad    = orm.EventPostLoad
	EventOnFlush     = orm.EventOnFlush
	EventPostFlush   = orm.EventPostFlush
	EventOnRollback  = orm.EventOnRollback
	EventPostBulk    = orm.EventPostBulk
)

// NewEventManager returns an event manager to share between entity managers.
func NewEventManager() *EventManager { return orm.NewEventManager() }

// WithEvents makes an entity manager use a shared event manager.
var WithEvents = orm.WithEvents
