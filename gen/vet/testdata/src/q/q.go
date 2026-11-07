package q

import "github.com/smatify/tenet"

type Stamps struct {
	createdAt string
}

type Post struct {
	tenet.Entity
	Stamps
	id     int64
	title  string
	author *Author `tenet:"joinColumn:writer"`
	slug   string  `tenet:"column:url_slug"`
	cache  string  `tenet:"-"`
}

type Author struct {
	tenet.Entity
	name string
}

const field = "title"

func queries(r *tenet.Repository[Post], dynamic string) {
	tenet.NewQuery[Post]().
		Where(tenet.Eq("title", 1), tenet.Eq(field, 1), tenet.Eq("author_id", 1), tenet.Eq("writer", 1), tenet.Eq("url_slug", 1)).
		Where(tenet.IsNull("created_at"), tenet.Eq("createdAt", 1), tenet.Eq("a.nmae", 1), tenet.Eq(dynamic, 1)).
		Where(tenet.Eq("titel", 1)).                         // want `Post has no field "titel"`
		Where(tenet.Or(tenet.IsNull("cache"))).              // want `Post has no field "cache"`
		Where(tenet.Raw("{title} = ? OR {nope} = ?", 1, 2)). // want `Post has no field "nope"`
		OrderBy(tenet.Raw("LOWER({titel})"), 0).             // want `Post has no field "titel"`
		OrderBy("slugg", 0).                                 // want `Post has no field "slugg"`
		Join("author", "a").Join("autor", "b").              // want `Post has no field "autor"`
		With("author", "author.posts", "autor.posts")        // want `Post has no field "autor"`
	r.FindBy(tenet.Criteria{"title": 1, "tilte": 2}, tenet.OrderBy("x", 0), tenet.With("author")) // want `Post has no field "tilte"` `Post has no field "x"`
	tenet.NewQuery[Author]().Where(tenet.Eq("title", 1))                                          // want `Author has no field "title"`
}
