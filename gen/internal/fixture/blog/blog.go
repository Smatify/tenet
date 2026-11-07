// Package blog is a fixture for the generator tests.
package blog

import (
	"database/sql"
	"reflect"
	"time"

	"github.com/smatify/tenet"
)

// List is a minimal collection container.
type List[T any] struct{ Items []*T }

// CollectionElem implements mapping.CollectionField.
func (List[T]) CollectionElem() reflect.Type { return reflect.TypeFor[T]() }

// Author uses the Go style with exported fields.
type Author struct {
	tenet.Entity `tenet:"table:authors"`
	ID           int64
	Email        string `tenet:"length:180,unique"`
	Nick         sql.NullString
	Born         *time.Time
	Prefs        map[string]any
	Address      Address    `tenet:"embedded,prefix:addr_"`
	Posts        List[Post] `tenet:"oneToMany,mappedBy:author"`
	cache        int        `tenet:"-"`
}

// Post uses the Doctrine style with unexported fields and traits.
type Post struct {
	tenet.Entity
	tenet.UUIDID
	tenet.Timestamps
	title  string  `tenet:"length:200"`
	author *Author `tenet:"manyToOne,inversedBy:Posts"`
	meta   Meta    `tenet:"type:json"`
}

// Content is the root of a single table hierarchy.
type Content struct {
	tenet.Entity `tenet:"inheritance:single"`
	ID           int64
	Title        string
}

// Video is a subclass of Content.
type Video struct {
	Content `tenet:"discriminatorValue:video"`
	Seconds int
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
