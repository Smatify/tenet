package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/schema"
	"github.com/smatify/tenet/validate"
)

func init() { Register(Case{"Validate/DatabaseSync", testValidate}) }

func testValidate(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	r := mapping.NewRegistry()
	if err := r.Register(mapAuthor{}); err != nil {
		t.Fatal(err)
	}
	entities := r.All()
	want := mapping.BuildSchema(entities)
	DropSchemaQuietly(db, want)
	t.Cleanup(func() { DropSchemaQuietly(db, want) })
	opts := validate.Options{Entities: entities, DB: db, Ignore: onlyPrefix("map_")}

	// Empty database: everything must be created.
	rep, err := validate.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DatabaseOK() || len(rep.Diff.CreateTables) != 4 || !rep.MappingOK() {
		t.Fatalf("report:\n%s", rep)
	}
	ApplySchema(t, db, rep.Statements)

	rep, err = validate.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("expected sync after applying statements:\n%s", rep)
	}

	// Drift: widen a column behind the ORM's back.
	drifted := want.Clone()
	drifted.Table("map_tag").Column("label").SetLength(99)
	stmts, err := schema.NewDDL(db.Dialect()).MigrateSchemas(want, drifted, nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplySchema(t, db, stmts)
	rep, err = validate.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DatabaseOK() || !strings.Contains(rep.String(), "change column label") {
		t.Fatalf("drift not detected:\n%s", rep)
	}
	ApplySchema(t, db, rep.Statements)
	if rep, _ = validate.Run(ctx, opts); !rep.OK() {
		t.Fatalf("not repaired:\n%s", rep)
	}
}
