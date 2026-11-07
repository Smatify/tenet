// Package model holds the entities of the tenet benchmarks. Regenerate
// entities_tenet.go from ../gen with
//
//	GOWORK=off go run ./cmd/tenet generate ../bench/model
package model

import "github.com/smatify/tenet"

// Author writes posts.
type Author struct {
	tenet.Entity `tenet:"table:author"`
	tenet.IntID
	name string `tenet:"length:100"`
}

// Post is written by an author.
type Post struct {
	tenet.Entity `tenet:"table:post"`
	tenet.IntID
	author *Author `tenet:"manyToOne,notnull"`
	title  string  `tenet:"length:200"`
	views  int32
}

// Name returns the name.
func (a *Author) Name() string {
	tenet.Touch(a)
	return a.name
}

// SetName sets the name.
func (a *Author) SetName(v string) *Author {
	tenet.Touch(a)
	a.name = v
	return a
}

// Author returns the author.
func (p *Post) Author() *Author {
	tenet.Touch(p)
	return p.author
}

// SetAuthor sets the author.
func (p *Post) SetAuthor(v *Author) *Post {
	tenet.Touch(p)
	p.author = v
	return p
}

// Title returns the title.
func (p *Post) Title() string {
	tenet.Touch(p)
	return p.title
}

// SetTitle sets the title.
func (p *Post) SetTitle(v string) *Post {
	tenet.Touch(p)
	p.title = v
	return p
}

// Views returns the views.
func (p *Post) Views() int32 {
	tenet.Touch(p)
	return p.views
}

// SetViews sets the views.
func (p *Post) SetViews(v int32) *Post {
	tenet.Touch(p)
	p.views = v
	return p
}
