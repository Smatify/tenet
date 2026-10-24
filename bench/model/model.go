// Package model holds the entities of the smorm benchmarks. Regenerate
// entities_smorm.go from ../gen with
//
//	GOWORK=off go run ./cmd/smorm generate ../bench/model
package model

import "github.com/smatify/smorm"

// Author writes posts.
type Author struct {
	smorm.Entity `smorm:"table:author"`
	smorm.IntID
	name string `smorm:"length:100"`
}

// Post is written by an author.
type Post struct {
	smorm.Entity `smorm:"table:post"`
	smorm.IntID
	author *Author `smorm:"manyToOne,notnull"`
	title  string  `smorm:"length:200"`
	views  int32
}

// Name returns the name.
func (a *Author) Name() string {
	smorm.Touch(a)
	return a.name
}

// SetName sets the name.
func (a *Author) SetName(v string) *Author {
	smorm.Touch(a)
	a.name = v
	return a
}

// Author returns the author.
func (p *Post) Author() *Author {
	smorm.Touch(p)
	return p.author
}

// SetAuthor sets the author.
func (p *Post) SetAuthor(v *Author) *Post {
	smorm.Touch(p)
	p.author = v
	return p
}

// Title returns the title.
func (p *Post) Title() string {
	smorm.Touch(p)
	return p.title
}

// SetTitle sets the title.
func (p *Post) SetTitle(v string) *Post {
	smorm.Touch(p)
	p.title = v
	return p
}

// Views returns the views.
func (p *Post) Views() int32 {
	smorm.Touch(p)
	return p.views
}

// SetViews sets the views.
func (p *Post) SetViews(v int32) *Post {
	smorm.Touch(p)
	p.views = v
	return p
}
