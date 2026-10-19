// Package blog is a fixture for the generator tests.
package blog

import (
	"database/sql"
	"reflect"
	"time"

	"github.com/smatify/smorm"
)

// List is a minimal collection container.
type List[T any] struct{ Items []*T }

// CollectionElem implements mapping.CollectionField.
func (List[T]) CollectionElem() reflect.Type { return reflect.TypeFor[T]() }

// Author uses the Go style with exported fields.
type Author struct {
	smorm.Entity `smorm:"table:authors"`
	ID           int64
	Email        string `smorm:"length:180,unique"`
	Nick         sql.NullString
	Born         *time.Time
	Prefs        map[string]any
	Address      Address    `smorm:"embedded,prefix:addr_"`
	Posts        List[Post] `smorm:"oneToMany,mappedBy:author"`
	cache        int        `smorm:"-"`
}

// Post uses the Doctrine style with unexported fields and traits.
type Post struct {
	smorm.Entity
	smorm.UUIDID
	smorm.Timestamps
	title  string  `smorm:"length:200"`
	author *Author `smorm:"manyToOne,inversedBy:Posts"`
	meta   Meta    `smorm:"type:json"`
}

// Meta is a plain embedded value stored as JSON.
type Meta struct {
	Tags []string
}

// NotAnEntity must be ignored.
type NotAnEntity struct{ A int }

// Address is an embeddable value object.
type Address struct {
	Street string
	City   string
}
