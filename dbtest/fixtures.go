package dbtest

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/console"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/fixtures"
	"github.com/smatify/smorm/mapping"
)

func init() { Register(Case{"Fixtures/LoadPurgeAppend", testFixtures}) }

func testFixtures(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, colAuthor{}, ooUser{}, ormCategory{})
	reg := newEM().Registry()
	fx := fixtures.NewRegistry()
	must(t, fx.Add(fixtures.Fixture{Name: "authors", Load: func(_ context.Context, f *fixtures.Context) error {
		for _, n := range []string{"ann", "bob"} {
			a := &colAuthor{Name: n}
			a.Tags.Add(&colTag{Label: "tag-" + n})
			f.AddReference("author-"+n, a)
			if err := f.EM.Persist(a); err != nil {
				return err
			}
		}
		return nil
	}}))
	must(t, fx.Add(fixtures.Fixture{Name: "posts", DependsOn: []string{"authors"}, Groups: []string{"content"}, Load: func(_ context.Context, f *fixtures.Context) error {
		ann := fixtures.Ref[colAuthor](f, "author-ann")
		return f.EM.Persist(&colPost{Title: "first", Author: ann})
	}}))
	must(t, fx.Add(fixtures.Fixture{Name: "tree", Load: func(_ context.Context, f *fixtures.Context) error {
		root := &ormCategory{Name: "root"}
		child := &ormCategory{Name: "child", Parent: root}
		if err := f.EM.Persist(root); err != nil {
			return err
		}
		return f.EM.Persist(child)
	}}))
	ents := reg.All()
	keep := func(t string) bool {
		for _, e := range ents {
			if strings.EqualFold(e.Table, t) {
				return false
			}
		}
		return !strings.HasPrefix(t, "col_") // keep everything unrelated
	}
	load := func(opts fixtures.Options) []string {
		t.Helper()
		opts.Entities, opts.Exclude = ents, keep
		done, err := fx.Load(ctx, db, reg, opts)
		must(t, err)
		return done
	}
	if done := load(fixtures.Options{}); strings.Join(done, ",") != "authors,posts,tree" {
		t.Fatalf("done = %v", done)
	}
	if Count(t, db, "col_author") != 2 || Count(t, db, "col_post") != 1 || Count(t, db, "col_author_tag") != 2 || Count(t, db, "orm_category") != 2 {
		t.Fatal("fixture rows")
	}
	// Loading again purges first (FKs, join tables and self references).
	load(fixtures.Options{})
	if Count(t, db, "col_author") != 2 || Count(t, db, "orm_category") != 2 {
		t.Fatal("purge before reload")
	}
	// Append keeps rows; groups pull in dependencies.
	if done := load(fixtures.Options{Append: true, Groups: []string{"content"}}); strings.Join(done, ",") != "authors,posts" {
		t.Fatalf("group = %v", done)
	}
	if Count(t, db, "col_author") != 4 {
		t.Fatal("append")
	}
	a, err := smorm.RepositoryFor[colAuthor](newEM()).FindOneBy(ctx, smorm.Criteria{"Name": "bob"})
	must(t, err)
	if a.Tags.Len() != 1 {
		t.Fatal("m2m from fixtures")
	}
	// Through the console.
	app := console.New(console.Config{DB: db, Registry: reg, Entities: []any{colAuthor{}, ooUser{}, ormCategory{}, ormAuthor{}},
		Fixtures: fx, IgnoreTables: keep, Migrations: nil})
	var out bytes.Buffer
	app.Out, app.Err = &out, &out
	app.In = strings.NewReader("no\n")
	if code := app.Run(ctx, []string{"fixtures:load"}); code != 0 || !strings.Contains(out.String(), "CANCELLED") {
		t.Fatalf("cancel: %s", out.String())
	}
	out.Reset()
	if code := app.Run(ctx, []string{"fixtures:load", "-n"}); code != 0 || !strings.Contains(out.String(), "Loaded 3 fixture(s)") {
		t.Fatalf("console: %s", out.String())
	}
	if Count(t, db, "col_author") != 2 {
		t.Fatal("console purge")
	}
	_ = mapping.Default
}
