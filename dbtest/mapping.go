package dbtest

import (
	"context"
	"testing"
	"time"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/internal/entity"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/schema"
	"github.com/smatify/tenet/uuid"
)

func init() { Register(Case{"Mapping/SchemaFromEntities", testMappingSchema}) }

type mapAuthor struct {
	entity.Header `tenet:"table:map_author"`
	ID            int64
	Email         string `tenet:"length:180,unique"`
	Name          string `tenet:"default:'anon'"`
	Born          *time.Time
	Prefs         map[string]any
	Posts         []*mapPost `tenet:"oneToMany,mappedBy:author"`
	Tags          []*mapTag  `tenet:"manyToMany,joinTable:map_author_tag"`
}

type mapPost struct {
	entity.Header `tenet:"table:map_post"`
	id            uuid.UUID  `tenet:"id"`
	title         string     `tenet:"length:200,index"`
	author        *mapAuthor `tenet:"manyToOne,inversedBy:Posts,notnull,onDelete:cascade"`
}

type mapTag struct {
	entity.Header `tenet:"table:map_tag"`
	ID            int32
	Label         string `tenet:"length:50,unique"`
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
