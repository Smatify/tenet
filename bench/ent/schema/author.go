// Package schema holds the ent schema of the benchmarks.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Author writes posts.
type Author struct{ ent.Schema }

// Annotations maps the table.
func (Author) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "author"}}
}

// Fields of Author.
func (Author) Fields() []ent.Field {
	return []ent.Field{field.Int64("id"), field.String("name").MaxLen(100)}
}

// Edges of Author.
func (Author) Edges() []ent.Edge {
	return []ent.Edge{edge.To("posts", Post.Type)}
}
