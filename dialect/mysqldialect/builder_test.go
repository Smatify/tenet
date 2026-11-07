package mysqldialect

import (
	"errors"
	"testing"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/internal/fakedb"
)

func builderDB(t *testing.T, d *Dialect) *dbal.DB {
	db := dbal.NewDB(fakedb.New().DB(), d)
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

func TestBuilderMySQL(t *testing.T) {
	db := builderDB(t, New())
	s, a, err := db.NewSelect().Table("t").Offset(5).Lock(dbal.LockForShare).SkipLocked().Build()
	check(t, s, a, err, "SELECT * FROM `t` LIMIT 18446744073709551615 OFFSET 5 FOR SHARE SKIP LOCKED")
	s, a, err = db.NewInsert().Table("t").Columns("k", "v").Values(1, 2).OnConflict("k").DoUpdate("v").Build()
	check(t, s, a, err, "INSERT INTO `t` (`k`, `v`) VALUES (?, ?) AS new ON DUPLICATE KEY UPDATE `v` = new.`v`")
	s, a, err = db.NewInsert().Table("t").Columns("k", "v").Values(1, 2).OnConflict("k").DoNothing().Build()
	check(t, s, a, err, "INSERT INTO `t` (`k`, `v`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `k` = `k`")
	s, a, err = db.NewDelete().TableAs("t", "x").Where("x.k = 1").Build()
	check(t, s, a, err, "DELETE `x` FROM `t` AS `x` WHERE (x.k = 1)")
	if _, _, err := db.NewInsert().Table("t").Values(1).Returning("id").Build(); !errors.Is(err, dbal.ErrReturningUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := db.NewUpdate().Table("t").Set("a", 1).Where("x").Returning("id").Build(); !errors.Is(err, dbal.ErrReturningUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := db.NewDelete().Table("t").Where("x").Returning("id").Build(); !errors.Is(err, dbal.ErrReturningUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := db.NewInsert().Table("t").Values(1).DoNothing().Build(); err == nil {
		t.Fatal("DoNothing without columns must fail")
	}
}

func TestBuilderMariaDB(t *testing.T) {
	db := builderDB(t, NewMariaDB())
	s, a, err := db.NewInsert().Table("t").Columns("k", "v").Values(1, 2).OnConflict("k").DoUpdate("v").Returning("id").Build()
	check(t, s, a, err, "INSERT INTO `t` (`k`, `v`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `v` = VALUES(`v`) RETURNING `id`")
	s, a, err = db.NewSelect().Table("t").Lock(dbal.LockForShare).Build()
	check(t, s, a, err, "SELECT * FROM `t` LOCK IN SHARE MODE")
	s, a, err = db.NewDelete().Table("t").Where("k = 1").Returning("k").Build()
	check(t, s, a, err, "DELETE FROM `t` WHERE (k = 1) RETURNING `k`")
	if _, _, err := db.NewDelete().TableAs("t", "x").Where("k = 1").Returning("k").Build(); !errors.Is(err, dbal.ErrReturningUnsupported) {
		t.Fatalf("err = %v", err)
	}
}
