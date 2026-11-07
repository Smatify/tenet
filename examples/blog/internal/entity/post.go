package entity

import "github.com/smatify/tenet"

// Post is an entity.
type Post struct {
	tenet.Entity `tenet:"table:post"`
	tenet.IntID
	title    string  `tenet:"length:200"`
	author   *Author `tenet:"manyToOne,inversedBy:posts,notnull"`
	subtitle *string `tenet:"length:255"`
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

// Subtitle returns the subtitle.
func (p *Post) Subtitle() *string {
	tenet.Touch(p)
	return p.subtitle
}

// SetSubtitle sets the subtitle.
func (p *Post) SetSubtitle(v *string) *Post {
	tenet.Touch(p)
	p.subtitle = v
	return p
}
