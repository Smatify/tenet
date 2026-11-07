package main

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect/sqlitedialect"
	"github.com/smatify/tenet/migrations"

	_ "example.com/blog/migrations"
)

func TestRun(t *testing.T) {
	ctx := context.Background()
	db, err := dbal.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)", sqlitedialect.New())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SQL().SetMaxOpenConns(1)
	if _, err := migrations.New(db, migrations.Default).Migrate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := run(ctx, db, &out); err != nil {
		t.Fatal(err)
	}
	want := `Hello by Ann
World by Ann
Bob wrote 1 post(s)
[{Name:Ann Posts:2} {Name:Bob Posts:1}]
3 posts marked as draft
`
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}
