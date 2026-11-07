package main

import (
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	ctx := context.Background()
	db, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var out strings.Builder
	if err := run(ctx, db, &out); err != nil {
		t.Fatal(err)
	}
	want := `imported 2 products
order of ann@example.com: 2 lines, 39.00 EUR
order of bob@example.com: TEE: out of stock
second change of MUG: true
MUG  sold 2 for 20.00 EUR
TEE  sold 1 for 19.00 EUR
`
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}
