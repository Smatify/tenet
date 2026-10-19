package a

import "github.com/smatify/smorm"

type Post struct {
	smorm.Entity
	id     int64
	title  string
	author *Author
	cache  string `smorm:"-"`
	Public string
}

type Author struct {
	smorm.Entity
	name string
}

type plain struct{ title string }

func (p *Post) Title() string {
	smorm.Touch(p)
	return p.title
}

func (p *Post) BadTitle() string {
	return p.title // want `Post.title is accessed without smorm.Touch\(p\)`
}

func (p *Post) TouchTooLate() string {
	t := p.title // want `Post.title is accessed without smorm.Touch\(p\)`
	smorm.Touch(p)
	return t
}

func (p *Post) SetTitle(v string) {
	p.title = v // want `Post.title is accessed without smorm.Touch\(p\)`
	p.author = nil
}

func (p *Post) ID() int64        { return p.id }
func (p *Post) Cache() string    { return p.cache }
func (p *Post) Exported() string { return p.Public }

func helper(a *Author, b *Author) string {
	smorm.Touch(a)
	return a.name + b.name // want `Author.name is accessed without smorm.Touch\(b\)`
}

// Ignored is excluded: smormvet:ignore
func Ignored(p *Post) string { return p.title }

func notEntity(x plain) string { return x.title }
