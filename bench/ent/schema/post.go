package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Post is written by an author.
type Post struct{ ent.Schema }

// Annotations maps the table.
func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "post"}}
}

// Fields of Post.
func (Post) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("author_id"),
		field.String("title").MaxLen(200),
		field.Int32("views").Default(0),
	}
}

// Edges of Post.
func (Post) Edges() []ent.Edge {
	return []ent.Edge{edge.From("author", Author.Type).Ref("posts").Field("author_id").Unique().Required()}
}
