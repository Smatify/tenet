package entity

import (
	"slices"

	"github.com/smatify/tenet"
)

// Author is an entity.
type Author struct {
	tenet.Entity `tenet:"table:author"`
	tenet.IntID
	posts tenet.Collection[Post] `tenet:"oneToMany,mappedBy:author"`
	name  string                 `tenet:"length:100,unique"`
}

// Posts returns the posts.
func (a *Author) Posts() []*Post {
	tenet.Touch(a)
	return a.posts.All()
}

// AddPost adds item to the posts.
func (a *Author) AddPost(item *Post) *Author {
	tenet.Touch(a)
	if !a.posts.Contains(item) {
		a.posts.Add(item)
		item.SetAuthor(a)
	}
	return a
}

// RemovePost removes item from the posts.
func (a *Author) RemovePost(item *Post) *Author {
	tenet.Touch(a)
	if a.posts.Remove(item) {
		if item.Author() == a {
			item.SetAuthor(nil)
		}
	}
	return a
}

// SetPosts replaces the posts with items.
func (a *Author) SetPosts(items []*Post) *Author {
	for _, item := range a.Posts() {
		if !slices.Contains(items, item) {
			a.RemovePost(item)
		}
	}
	for _, item := range items {
		a.AddPost(item)
	}
	return a
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
