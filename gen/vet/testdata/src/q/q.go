package q

import "github.com/smatify/smorm"

type Stamps struct {
	createdAt string
}

type Post struct {
	smorm.Entity
	Stamps
	id     int64
	title  string
	author *Author `smorm:"joinColumn:writer"`
	slug   string  `smorm:"column:url_slug"`
	cache  string  `smorm:"-"`
}

type Author struct {
	smorm.Entity
	name string
}

const field = "title"

func queries(r *smorm.Repository[Post], dynamic string) {
	smorm.NewQuery[Post]().
		Where(smorm.Eq("title", 1), smorm.Eq(field, 1), smorm.Eq("author_id", 1), smorm.Eq("writer", 1), smorm.Eq("url_slug", 1)).
		Where(smorm.IsNull("created_at"), smorm.Eq("createdAt", 1), smorm.Eq("a.nmae", 1), smorm.Eq(dynamic, 1)).
		Where(smorm.Eq("titel", 1)).                         // want `Post has no field "titel"`
		Where(smorm.Or(smorm.IsNull("cache"))).              // want `Post has no field "cache"`
		Where(smorm.Raw("{title} = ? OR {nope} = ?", 1, 2)). // want `Post has no field "nope"`
		OrderBy(smorm.Raw("LOWER({titel})"), 0).             // want `Post has no field "titel"`
		OrderBy("slugg", 0).                                 // want `Post has no field "slugg"`
		Join("author", "a").Join("autor", "b").              // want `Post has no field "autor"`
		With("author", "author.posts", "autor.posts")        // want `Post has no field "autor"`
	r.FindBy(smorm.Criteria{"title": 1, "tilte": 2}, smorm.OrderBy("x", 0), smorm.With("author")) // want `Post has no field "tilte"` `Post has no field "x"`
	smorm.NewQuery[Author]().Where(smorm.Eq("title", 1))                                          // want `Author has no field "title"`
}
