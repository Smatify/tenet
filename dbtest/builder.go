package dbtest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
)

func init() {
	Register(
		Case{"Builder/SelectInsert", testBuilderSelect},
		Case{"Builder/Returning", testBuilderReturning},
		Case{"Builder/Upsert", testBuilderUpsert},
		Case{"Builder/UpdateDelete", testBuilderUpdateDelete},
		Case{"Builder/CTEUnionSubquery", testBuilderCTE},
		Case{"Builder/Locking", testBuilderLocking},
	)
}

const peopleCols = "id INT PRIMARY KEY, name VARCHAR(50) NOT NULL, score INT NOT NULL"

func seedPeople(t *testing.T, db *dbal.DB) string {
	t.Helper()
	tbl := Table(t, db, peopleCols)
	q := db.NewInsert().Table(tbl).Columns("id", "name", "score")
	for i, n := range []string{"ann", "bob", "cid", "dan", "eve"} {
		q.Values(i+1, n, (i+1)*10)
	}
	if _, err := q.Exec(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return tbl
}

func names(t *testing.T, q *dbal.SelectQuery) []string {
	t.Helper()
	var out []string
	err := q.ForEach(context.Background(), func(rows *sql.Rows) error {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		out = append(out, n)
		return nil
	})
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func testBuilderSelect(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	tbl := seedPeople(t, db)

	eq(t, names(t, db.NewSelect().Column("name").Table(tbl).Where("score > ?", 15).OrderBy("score", dbal.Desc).Limit(2).Offset(1)),
		[]string{"dan", "cid"})
	eq(t, names(t, db.NewSelect().Column("p.name").TableAs(tbl, "p").Where("p.name IN ?", dbal.In([]string{"bob", "eve"})).OrderBy("p.name", dbal.Asc)),
		[]string{"bob", "eve"})
	eq(t, names(t, db.NewSelect().Column("name").Table(tbl).OrderBy("id", dbal.Asc).Offset(3)), []string{"dan", "eve"})
	eq(t, names(t, db.NewSelect().Column("name").Table(tbl).Where("name IN ?", dbal.In([]string{}))), nil)
	eq(t, names(t, db.NewSelect().Column("name").Table(tbl).Where("score < ?", 25).WhereOrGroup(func(w *dbal.WhereBuilder) {
		w.Where("score > ?", 45)
	}).OrderBy("id", dbal.Asc)), []string{"ann", "bob", "eve"})

	n, err := db.NewSelect().Table(tbl).Where("score >= ?", 30).OrderBy("id", dbal.Asc).Limit(1).Count(ctx)
	if err != nil || n != 3 {
		t.Fatalf("Count = %d, %v", n, err)
	}
	n, err = db.NewSelect().Column("score").Table(tbl).GroupBy("score").Having("COUNT(*) > ?", 0).Count(ctx)
	if err != nil || n != 5 {
		t.Fatalf("grouped Count = %d, %v", n, err)
	}
	ok, err := db.NewSelect().Table(tbl).Where("name = ?", "eve").Exists(ctx)
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	ok, err = db.NewSelect().Table(tbl).Where("name = ?", "zed").Exists(ctx)
	if err != nil || ok {
		t.Fatalf("Exists(zed) = %v, %v", ok, err)
	}
	maps, err := db.NewSelect().Column("id", "name").Table(tbl).Where("id = ?", 1).ScanMaps(ctx)
	if err != nil || len(maps) != 1 {
		t.Fatalf("ScanMaps = %v, %v", maps, err)
	}
	var name string
	if err := db.NewSelect().Column("name").Table(tbl).Where("id = ?", 404).Scan(ctx, &name); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Scan no rows = %v", err)
	}
}

func testBuilderReturning(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	f := db.Dialect().Features()
	tbl := Table(t, db, peopleCols)
	q := db.NewInsert().Table(tbl).Columns("id", "name", "score").Values(7, "x", 1).Returning("id", "name")
	if !f.Has(dialect.FeatureInsertReturning) && !f.Has(dialect.FeatureOutput) {
		if _, err := q.Exec(ctx); !errors.Is(err, dbal.ErrReturningUnsupported) {
			t.Fatalf("err = %v", err)
		}
		return
	}
	var id int
	var name string
	if err := q.Scan(ctx, &id, &name); err != nil || id != 7 || name != "x" {
		t.Fatalf("returning = %d %q %v", id, name, err)
	}
}

func testBuilderUpsert(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	tbl := seedPeople(t, db)
	if _, err := db.NewInsert().Table(tbl).Columns("id", "name", "score").Values(1, "ANN", 99).Values(9, "new", 1).
		OnConflict("id").DoUpdate("name", "score").Exec(ctx); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	if _, err := db.NewInsert().Table(tbl).Columns("id", "name", "score").Values(2, "ignored", 0).
		OnConflict("id").DoNothing().Exec(ctx); err != nil {
		t.Fatalf("upsert nothing: %v", err)
	}
	if _, err := db.NewInsert().Table(tbl).Columns("id", "name", "score").Values(3, "c", 5).
		OnConflict("id").DoUpdateSet("score", "? + 1", dbal.Excluded("score")).Exec(ctx); err != nil {
		t.Fatalf("upsert expr: %v", err)
	}
	eq(t, names(t, db.NewSelect().Column("name").Table(tbl).Where("id IN ?", dbal.In([]int{1, 2, 9})).OrderBy("id", dbal.Asc)),
		[]string{"ANN", "bob", "new"})
	var score int
	if err := db.NewSelect().Column("score").Table(tbl).Where("id = 3").Scan(ctx, &score); err != nil || score != 6 {
		t.Fatalf("score = %d, %v", score, err)
	}
}

func testBuilderUpdateDelete(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	tbl := seedPeople(t, db)

	res, err := db.NewUpdate().TableAs(tbl, "p").Set("name", "BOB").SetExpr("score = score + ?", 1).Where("id = ?", 2).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("affected = %d", n)
	}
	if _, err := db.NewUpdate().Table(tbl).Set("score", 0).Exec(ctx); !errors.Is(err, dbal.ErrNoWhere) {
		t.Fatalf("err = %v", err)
	}
	canUpdateReturn := d.Features().Has(dialect.FeatureReturning) || d.Name() == dialect.MSSQL
	if canUpdateReturn {
		var score int
		if err := db.NewUpdate().Table(tbl).Set("score", 77).Where("id = ?", 3).Returning("score").Scan(ctx, &score); err != nil || score != 77 {
			t.Fatalf("update returning = %d, %v", score, err)
		}
	}

	res, err = db.NewDelete().TableAs(tbl, "p").Where("p.id = ?", 5).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("deleted = %d", n)
	}
	canDeleteReturn := canUpdateReturn || d.Name() == dialect.MariaDB
	if canDeleteReturn {
		var name string
		if err := db.NewDelete().Table(tbl).Where("id = ?", 4).Returning("name").Scan(ctx, &name); err != nil || name != "dan" {
			t.Fatalf("delete returning = %q, %v", name, err)
		}
	}
	if _, err := db.NewDelete().Table(tbl).AllRows().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n := Count(t, db, tbl); n != 0 {
		t.Fatalf("count = %d", n)
	}
}

func testBuilderCTE(t *testing.T, db *dbal.DB) {
	tbl := seedPeople(t, db)
	top := db.NewSelect().Column("id").Table(tbl).Where("score >= ?", 40)
	eq(t, names(t, db.NewSelect().With("best", top).Column("name").Table(tbl).Where("id IN (SELECT id FROM best)").OrderBy("name", dbal.Asc)),
		[]string{"dan", "eve"})
	eq(t, names(t, db.NewSelect().Column("name").Table(tbl).Where("id IN ?", top).OrderBy("name", dbal.Desc)),
		[]string{"eve", "dan"})
	u := db.NewSelect().Column("name").Table(tbl).Where("id = 1").
		Union(db.NewSelect().Column("name").Table(tbl).Where("id = 2")).
		UnionAll(db.NewSelect().Column("name").Table(tbl).Where("id = 2")).OrderExpr("1")
	eq(t, names(t, u), []string{"ann", "bob", "bob"})
	eq(t, names(t, db.NewSelect().Column("s.name").TableExpr("? AS s", db.NewSelect().Column("name").Table(tbl).Where("id = 3"))),
		[]string{"cid"})
}

func testBuilderLocking(t *testing.T, db *dbal.DB) {
	tbl := seedPeople(t, db)
	err := db.RunInTx(context.Background(), nil, func(ctx context.Context, tx *dbal.Tx) error {
		var name string
		return tx.NewSelect().Column("name").Table(tbl).Where("id = ?", 1).ForUpdate().Scan(ctx, &name)
	})
	if err != nil {
		t.Fatal(err)
	}
}
