package dbtest

import (
	"context"
	"testing"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/uuid"
)

func init() { Register(Case{"Mapping/SchemaFromEntities", testMappingSchema}) }

type mapAuthor struct {
	entity.Header `smorm:"table:map_author"`
	ID            int64
	Email         string `smorm:"length:180,unique"`
	Name          string `smorm:"default:'anon'"`
	Born          *time.Time
	Prefs         map[string]any
	Posts         []*mapPost `smorm:"oneToMany,mappedBy:author"`
	Tags          []*mapTag  `smorm:"manyToMany,joinTable:map_author_tag"`
}

type mapPost struct {
	entity.Header `smorm:"table:map_post"`
	id            uuid.UUID  `smorm:"id"`
	title         string     `smorm:"length:200,index"`
	author        *mapAuthor `smorm:"manyToOne,inversedBy:Posts,notnull,onDelete:cascade"`
}

type mapTag struct {
	entity.Header `smorm:"table:map_tag"`
	ID            int32
	Label         string `smorm:"length:50,unique"`
}

func testMappingSchema(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	r := mapping.NewRegistry()
	if err := r.Register(mapAuthor{}); err != nil {
		t.Fatal(err)
	}
	want := mapping.BuildSchema(r.All())
	if err := want.Validate(); err != nil {
		t.Fatal(err)
	}
	DropSchemaQuietly(db, want)
	t.Cleanup(func() { DropSchemaQuietly(db, want) })
	stmts, err := schema.NewDDL(d).CreateSchema(want)
	if err != nil {
		t.Fatal(err)
	}
	ApplySchema(t, db, stmts)
	got, err := schema.Introspect(ctx, db, d)
	if err != nil {
		t.Fatal(err)
	}
	if diff := schema.NewComparator(d).IgnoreTables(onlyPrefix("map_")).Compare(got, want); !diff.IsEmpty() {
		t.Fatalf("mapped schema differs:\n%s", DescribeDiff(diff))
	}
}
