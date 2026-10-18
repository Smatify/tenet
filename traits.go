package smorm

import (
	"time"

	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/uuid"
)

// Entity marks a struct as an entity. Embed it as the first field and use its
// tag for entity options:
//
//	type User struct {
//		smorm.Entity `smorm:"table:users"`
//		smorm.IntID
//		email string `smorm:"length:180,unique"`
//	}
type Entity = entity.Header

// IntID is a trait for an auto-incremented BIGINT primary key.
type IntID struct {
	id int64 `smorm:"column:id,id"`
}

// ID returns the identifier; it is 0 until the entity has been flushed.
func (i *IntID) ID() int64 { return i.id }

// UUIDID is a trait for a UUIDv7 primary key generated on persist.
type UUIDID struct {
	id uuid.UUID `smorm:"column:id,id"`
}

// ID returns the identifier; it is the Nil UUID until the entity is persisted.
func (u *UUIDID) ID() uuid.UUID { return u.id }

// Timestamps is a trait maintaining created_at and updated_at columns. Both
// are set when the entity is inserted; updated_at changes on every update.
type Timestamps struct {
	createdAt time.Time `smorm:"column:created_at"`
	updatedAt time.Time `smorm:"column:updated_at"`
}

// CreatedAt returns when the entity was inserted.
func (t *Timestamps) CreatedAt() time.Time { return t.createdAt }

// UpdatedAt returns when the entity was last written.
func (t *Timestamps) UpdatedAt() time.Time { return t.updatedAt }

// TouchCreated implements mapping.CreatedToucher.
func (t *Timestamps) TouchCreated(now time.Time) {
	if t.createdAt.IsZero() {
		t.createdAt = now
	}
	t.updatedAt = now
}

// TouchUpdated implements mapping.UpdatedToucher.
func (t *Timestamps) TouchUpdated(now time.Time) { t.updatedAt = now }
