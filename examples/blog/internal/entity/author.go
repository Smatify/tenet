package entity

import "github.com/smatify/smorm"

// Author is an entity.
type Author struct {
	smorm.Entity `smorm:"table:author"`
	smorm.IntID
	posts smorm.Collection[Post] `smorm:"oneToMany,mappedBy:author"`
	name  string                 `smorm:"length:100,unique"`
}

// Posts returns the posts.
func (a *Author) Posts() []*Post {
	smorm.Touch(a)
	return a.posts.All()
}

// AddPost adds item to the posts.
func (a *Author) AddPost(item *Post) *Author {
	smorm.Touch(a)
	if !a.posts.Contains(item) {
		a.posts.Add(item)
		item.SetAuthor(a)
	}
	return a
}

// RemovePost removes item from the posts.
func (a *Author) RemovePost(item *Post) *Author {
	smorm.Touch(a)
	if a.posts.Remove(item) {
		if item.Author() == a {
			item.SetAuthor(nil)
		}
	}
	return a
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
