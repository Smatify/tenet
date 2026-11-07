package pgdialect

import (
	"testing"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/internal/fakedb"
)

func builderDB(t *testing.T) *dbal.DB {
	db := dbal.NewDB(fakedb.New().DB(), New())
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func check(t *testing.T, got string, _ []any, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("\n got: %s\nwant: %s", got, want)
	}
}

func TestBuilderSQL(t *testing.T) {
	db := builderDB(t)
	s, a, err := db.NewSelect().Table("t").Where("a = ? AND b = ?", 1, 2).Offset(5).ForUpdate().SkipLocked().Build()
	check(t, s, a, err, `SELECT * FROM "t" WHERE (a = $1 AND b = $2) OFFSET 5 FOR UPDATE SKIP LOCKED`)
	s, a, err = db.NewInsert().Table("t").Columns("k", "v").Values(1, 2).OnConflict("k").DoUpdate("v").Returning("id").Build()
	check(t, s, a, err, `INSERT INTO "t" ("k", "v") VALUES ($1, $2) ON CONFLICT ("k") DO UPDATE SET "v" = EXCLUDED."v" RETURNING "id"`)
	s, a, err = db.NewUpdate().Table("t").Set("v", 1).Where("k = ?", 2).Returning("v").Build()
	check(t, s, a, err, `UPDATE "t" SET "v" = $1 WHERE (k = $2) RETURNING "v"`)
	s, a, err = db.NewDelete().TableAs("t", "x").Where("x.k = ?", 2).Returning("k").Build()
	check(t, s, a, err, `DELETE FROM "t" AS "x" WHERE (x.k = $1) RETURNING "k"`)
}
