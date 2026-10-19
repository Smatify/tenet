package entity

import "github.com/smatify/smorm"

// Post is an entity.
type Post struct {
	smorm.Entity `smorm:"table:post"`
	smorm.IntID
	title    string  `smorm:"length:200"`
	author   *Author `smorm:"manyToOne,inversedBy:posts,notnull"`
	subtitle *string `smorm:"length:255"`
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

// Subtitle returns the subtitle.
func (p *Post) Subtitle() *string {
	smorm.Touch(p)
	return p.subtitle
}

// SetSubtitle sets the subtitle.
func (p *Post) SetSubtitle(v *string) *Post {
	smorm.Touch(p)
	p.subtitle = v
	return p
}
