package dbal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/fakedb"
	"github.com/smatify/smorm/internal/testdialect"
)

func expectSQL(t *testing.T, got string, args []any, err error, want string, wantArgs ...any) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("SQL mismatch\n got: %s\nwant: %s", got, want)
	}
	if len(wantArgs) == 0 {
		wantArgs = nil
	}
	if len(args) == 0 {
		args = nil
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestWriteExpr(t *testing.T) {
	d := testdialect.New()
	d.Dollar = true
	s, args, err := Build(d, SQL("a = ? AND b IN ? AND c = ? AND d ?? 'x?' AND e = ?",
		1, In([]int{2, 3}), Ident("u.name"), Safe("NOW()")))
	expectSQL(t, s, args, err, `a = $1 AND b IN ($2, $3) AND c = "u"."name" AND d ? 'x?' AND e = NOW()`, 1, 2, 3)

	if _, _, err := Build(d, SQL("a = ?")); !errors.Is(err, ErrArgCount) {
		t.Fatalf("missing arg: %v", err)
	}
	if _, _, err := Build(d, SQL("a = 1", 5)); !errors.Is(err, ErrArgCount) {
		t.Fatalf("extra arg: %v", err)
	}
	s, args, err = Build(d, SQL("x IN ?", In([]string{})))
	expectSQL(t, s, args, err, "x IN (NULL)")
	if _, _, err := Build(d, SQL("x IN ?", In(5))); err == nil {
		t.Fatal("In(non-slice) should fail")
	}
	s, args, err = Build(d, SQL("x IN ?", In([2]int{7, 8})))
	expectSQL(t, s, args, err, "x IN ($1, $2)", 7, 8)
	s, args, err = Build(d, SQL("x IN ?", In([]any{7, 8, 7, 7, 8})))
	expectSQL(t, s, args, err, "x IN ($1, $2)", 7, 8) // repeated values are bound once
	s, args, err = Build(d, Ident("t.*"))
	expectSQL(t, s, args, err, `"t".*`)
	if _, _, err := Build(d, SQL("? ?", SQL("?"), 1)); err == nil {
		t.Fatal("nested error must propagate")
	}
	if _, _, err := Build(d, SQL("x IN ?", In([]any{SQL("?")}))); err == nil {
		t.Fatal("error inside In must propagate")
	}
	w := NewSQLWriter(d)
	_ = w.WriteByte('x')
	if w.String() != "x" || w.Dialect() != d {
		t.Fatal("writer accessors")
	}
}

func newBuilderDB(t *testing.T) (*DB, *testdialect.Dialect) {
	t.Helper()
	d := testdialect.New()
	db, _ := newTestDB(t)
	db.dialect = d
	return db, d
}

func TestSelectFull(t *testing.T) {
	db, _ := newBuilderDB(t)
	sub := db.NewSelect().Column("id").Table("banned").Where("reason = ?", "spam")
	q := db.NewSelect().
		With("recent", db.NewSelect().Table("posts").Where("created > ?", 10)).
		Distinct().
		Column("u.id", "u.name").ColumnExpr("COUNT(p.id) AS n").
		TableAs("users", "u").
		LeftJoin("recent", "p", "p.user_id = u.id AND p.kind = ?", "x").
		Join("JOIN roles AS r ON r.id = u.role_id").
		Where("u.active = ?", true).
		WhereGroup(func(w *WhereBuilder) {
			w.Where("u.age > ?", 18).WhereOr("u.verified").WhereOrGroup(func(w *WhereBuilder) { w.Where("a").Where("b") })
		}).
		Where("u.id NOT IN ?", sub).
		GroupBy("u.id", "u.name").Having("COUNT(p.id) > ?", 2).
		OrderBy("u.name", Asc).OrderBy("n", Desc).OrderExpr("u.id").
		Limit(10).Offset(20).ForUpdate().SkipLocked().NoWait()
	s, args, err := q.Build()
	expectSQL(t, s, args, err,
		`WITH "recent" AS (SELECT * FROM "posts" WHERE (created > ?)) SELECT DISTINCT "u"."id", "u"."name", COUNT(p.id) AS n `+
			`FROM "users" AS "u" LEFT JOIN "recent" AS "p" ON p.user_id = u.id AND p.kind = ? JOIN roles AS r ON r.id = u.role_id `+
			`WHERE (u.active = ?) AND ((u.age > ?) OR (u.verified) OR ((a) AND (b))) AND (u.id NOT IN (SELECT "id" FROM "banned" WHERE (reason = ?))) `+
			`GROUP BY "u"."id", "u"."name" HAVING (COUNT(p.id) > ?) ORDER BY "u"."name" ASC, "n" DESC, u.id LIMIT 10 OFFSET 20 FOR UPDATE SKIP LOCKED NOWAIT`,
		10, "x", true, 18, "spam", 2)
	if !strings.HasPrefix(q.String(), "WITH") {
		t.Fatal("String()")
	}
}

func TestSelectVariants(t *testing.T) {
	db, _ := newBuilderDB(t)
	cases := []struct {
		q    *SelectQuery
		want string
	}{
		{db.NewSelect().ColumnExpr("1"), "SELECT 1"},
		{db.NewSelect().Table("a").Table("b").GroupByExpr("x").DistinctOn("x"), `SELECT DISTINCT ON (x) * FROM "a", "b" GROUP BY x`},
		{db.NewSelect().Table("t").Offset(5), `SELECT * FROM "t" OFFSET 5`},
		{db.NewSelect().Table("t").Limit(0), `SELECT * FROM "t" LIMIT 0`},
		{db.NewSelect().TableExpr("? AS s", db.NewSelect().Table("x")), `SELECT * FROM (SELECT * FROM "x") AS s`},
		{db.NewSelect().Table("t").InnerJoin("o", "", "o.id = t.id"), `SELECT * FROM "t" INNER JOIN "o" ON o.id = t.id`},
		{db.NewSelect().Table("t").Lock(LockForShare), `SELECT * FROM "t" FOR SHARE`},
		{db.NewSelect().Table("a").Union(db.NewSelect().Table("b")).UnionAll(db.NewSelect().Table("c")).
			Intersect(db.NewSelect().Table("d")).Except(db.NewSelect().Table("e")).OrderExpr("1").Limit(3),
			`SELECT * FROM "a" UNION SELECT * FROM "b" UNION ALL SELECT * FROM "c" INTERSECT SELECT * FROM "d" EXCEPT SELECT * FROM "e" ORDER BY 1 LIMIT 3`},
		{db.NewSelect().WithRecursive("r", SQL("SELECT 1")).Table("r"), `WITH RECURSIVE "r" AS (SELECT 1) SELECT * FROM "r"`},
		{db.NewSelect().Table("t").OrderBy("x", "bogus"), `SELECT * FROM "t" ORDER BY "x" ASC`},
		{db.NewSelect().Table("t").WhereOr("a").WhereExpr(SQL("b")).WhereGroup(func(*WhereBuilder) {}),
			`SELECT * FROM "t" WHERE (a) AND (b)`},
		{db.NewSelect().Table("t").Where("a").WhereOrGroup(func(w *WhereBuilder) { w.WhereExpr(SQL("b")).WhereGroup(func(w *WhereBuilder) { w.Where("c") }) }),
			`SELECT * FROM "t" WHERE (a) OR ((b) AND ((c)))`},
	}
	for _, c := range cases {
		s, args, err := c.q.Build()
		expectSQL(t, s, args, err, c.want)
	}
}

func TestSelectErrors(t *testing.T) {
	db, _ := newBuilderDB(t)
	boom := errors.New("boom")
	checks := []*SelectQuery{
		db.NewSelect().Err(boom).Err(errors.New("second")),
		db.NewSelect().Table("t").Union(db.NewSelect().Err(boom)),
		db.NewSelect().With("x", SQL("?")),
		db.NewSelect().With("x", db.NewSelect().Err(boom)),
		db.NewSelect().DistinctOn("?"),
		db.NewSelect().ColumnExpr("?"),
		db.NewSelect().TableExpr("?"),
		db.NewSelect().Join("?"),
		db.NewSelect().LeftJoin("t", "a", "?"),
		db.NewSelect().Where("?"),
		db.NewSelect().WhereGroup(func(w *WhereBuilder) { w.Where("?") }),
		db.NewSelect().GroupByExpr("?"),
		db.NewSelect().Having("?"),
		db.NewSelect().OrderExpr("?"),
		db.NewSelect().Union(db.NewSelect().Where("?")),
	}
	for i, q := range checks {
		if _, _, err := q.Build(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if !strings.HasPrefix(db.NewSelect().Err(boom).String(), "error: ") {
		t.Fatal("String should render the error")
	}
	ctx := context.Background()
	q := db.NewSelect().Err(boom)
	if _, err := q.Rows(ctx); !errors.Is(err, boom) {
		t.Fatal("Rows")
	}
	if err := q.Scan(ctx); !errors.Is(err, boom) {
		t.Fatal("Scan")
	}
	if err := q.ForEach(ctx, nil); !errors.Is(err, boom) {
		t.Fatal("ForEach")
	}
	if wrapped := (&SelectQuery{db: db, limit: -1, columns: []Expr{q}}); wrapped.String() == "" {
		t.Fatal("sub query error")
	}
}

func TestSelectClone(t *testing.T) {
	db, _ := newBuilderDB(t)
	a := db.NewSelect().Table("t").Where("a")
	b := a.Clone().Where("b")
	if a.String() == b.String() {
		t.Fatal("clone shares where")
	}
}

func TestCountQuery(t *testing.T) {
	db, _ := newBuilderDB(t)
	q := db.NewSelect().Column("id").Table("t").Where("x = ?", 1).OrderBy("id", Asc).Limit(5).Offset(10).ForUpdate()
	s, args, err := q.CountQuery().Build()
	expectSQL(t, s, args, err, `SELECT COUNT(*) FROM "t" WHERE (x = ?)`, 1)

	g := db.NewSelect().With("c", SQL("SELECT 1")).Column("a").Table("t").GroupBy("a").OrderBy("a", Asc)
	s, args, err = g.CountQuery().Build()
	expectSQL(t, s, args, err, `WITH "c" AS (SELECT 1) SELECT COUNT(*) FROM (SELECT "a" FROM "t" GROUP BY "a") AS dbal_count`)
}

func TestSelectExecution(t *testing.T) {
	rec := fakedb.New()
	db := NewDB(rec.DB(), testdialect.New())
	ctx := context.Background()
	rec.ReturnsOnce("COUNT(*)", []string{"n"}, []driver.Value{int64(42)})
	n, err := db.NewSelect().Table("t").Count(ctx)
	if err != nil || n != 42 {
		t.Fatalf("Count = %d, %v", n, err)
	}
	rec.ReturnsOnce("EXISTS", []string{"e"}, []driver.Value{int64(1)})
	ok, err := db.NewSelect().Table("t").OrderBy("x", Asc).Exists(ctx)
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if got := rec.Queries()[1]; got != `SELECT CASE WHEN EXISTS (SELECT * FROM "t") THEN 1 ELSE 0 END` {
		t.Fatalf("exists sql = %s", got)
	}
	rec.ReturnsOnce("FROM \"m\"", []string{"id", "b"}, []driver.Value{int64(1), []byte("x")}, []driver.Value{int64(2), nil})
	maps, err := db.NewSelect().Table("m").ScanMaps(ctx)
	if err != nil || len(maps) != 2 || maps[0]["id"] != int64(1) || string(maps[0]["b"].([]byte)) != "x" || maps[1]["b"] != nil {
		t.Fatalf("ScanMaps = %v, %v", maps, err)
	}
	rec.ReturnsOnce("FROM \"f\"", []string{"id"}, []driver.Value{int64(1)}, []driver.Value{int64(2)})
	stop := errors.New("stop")
	err = db.NewSelect().Table("f").ForEach(ctx, func(*sql.Rows) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("ForEach err = %v", err)
	}
	rec.ReturnsOnce("FROM \"bad\"", []string{"a", "b"}, []driver.Value{int64(1), int64(2)})
	err = db.NewSelect().Table("bad").ForEach(ctx, func(r *sql.Rows) error { var x int; return r.Scan(&x) })
	if err == nil {
		t.Fatal("scan mismatch should fail")
	}
}

func TestInsert(t *testing.T) {
	db, _ := newBuilderDB(t)
	s, args, err := db.NewInsert().With("c", SQL("SELECT 1")).Table("users").Columns("name", "age").
		Values("a", 1).Values("b", Safe("DEFAULT")).Returning("id").Build()
	expectSQL(t, s, args, err, `WITH "c" AS (SELECT 1) INSERT INTO "users" ("name", "age") VALUES (?, ?), (?, DEFAULT) RETURNING "id"`, "a", 1, "b")

	s, args, err = db.NewInsert().Table("u").Columns("email", "name").Values("e", "n").
		OnConflict("email").DoUpdate("name").DoUpdateSet("hits", "u.hits + ?", 1).Build()
	expectSQL(t, s, args, err, `INSERT INTO "u" ("email", "name") VALUES (?, ?) ON CONFLICT ("email") DO UPDATE SET "name" = EXCLUDED."name", "hits" = u.hits + ?`, "e", "n", 1)

	s, args, err = db.NewInsert().Table("u").Columns("id").Values(1).OnConflict().DoNothing().Build()
	expectSQL(t, s, args, err, `INSERT INTO "u" ("id") VALUES (?) ON CONFLICT DO NOTHING`, 1)

	s, args, err = db.NewInsert().Table("u").Columns("id").Select(db.NewSelect().Column("id").Table("v")).Build()
	expectSQL(t, s, args, err, `INSERT INTO "u" ("id") SELECT "id" FROM "v"`)

	s, args, err = db.NewInsert().Table("u").Values(1).Build()
	expectSQL(t, s, args, err, `INSERT INTO "u" VALUES (?)`, 1)

	errCases := []*InsertQuery{
		db.NewInsert().Table("u"),
		db.NewInsert().Table("u").Columns("a", "b").Values(1),
		db.NewInsert().Table("u").Columns("a").Values(1).DoUpdate("a"),
		db.NewInsert().Table("u").Values(SQL("?")),
		db.NewInsert().Table("u").Select(db.NewSelect().Where("?")),
		db.NewInsert().With("c", SQL("?")).Table("u").Values(1),
		db.NewInsert().Table("u").Columns("a").Values(1).OnConflict("a").DoUpdateSet("a", "?"),
	}
	for i, q := range errCases {
		if _, _, err := q.Build(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if !strings.HasPrefix(db.NewInsert().String(), "error: ") || !strings.HasPrefix(db.NewInsert().Table("u").Values(1).String(), "INSERT") {
		t.Fatal("String")
	}
}

func TestInsertNoReturning(t *testing.T) {
	db, d := newBuilderDB(t)
	d.F &^= dialect.FeatureInsertReturning | dialect.FeatureOutput
	if _, _, err := db.NewInsert().Table("u").Values(1).Returning("id").Build(); !errors.Is(err, ErrReturningUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdate(t *testing.T) {
	db, _ := newBuilderDB(t)
	s, args, err := db.NewUpdate().With("c", SQL("SELECT 1")).TableAs("users", "u").Set("name", "x").Set("updated", Safe("NOW()")).
		SetExpr("hits = hits + ?", 1).Where("id = ?", 5).WhereOr("x").WhereExpr(SQL("y")).
		WhereGroup(func(w *WhereBuilder) { w.Where("z") }).Returning("id", "name").Build()
	expectSQL(t, s, args, err, `WITH "c" AS (SELECT 1) UPDATE "users" AS "u" SET "name" = ?, "updated" = NOW(), hits = hits + ? WHERE (id = ?) OR (x) AND (y) AND ((z)) RETURNING "id", "name"`, "x", 1, 5)

	s, args, err = db.NewUpdate().Table("t").SetExpr("a = 1").AllRows().Build()
	expectSQL(t, s, args, err, `UPDATE "t" SET a = 1`)

	if _, _, err := db.NewUpdate().Table("t").Set("a", 1).Build(); !errors.Is(err, ErrNoWhere) {
		t.Fatalf("err = %v", err)
	}
	errCases := []*UpdateQuery{
		db.NewUpdate().Table("t").Where("x"),
		db.NewUpdate().Table("t").Set("a", SQL("?")).Where("x"),
		db.NewUpdate().Table("t").SetExpr("?").Where("x"),
		db.NewUpdate().Table("t").Set("a", 1).Where("?"),
		db.NewUpdate().With("c", SQL("?")).Table("t").Set("a", 1).Where("x"),
	}
	for i, q := range errCases {
		if _, _, err := q.Build(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if !strings.HasPrefix(db.NewUpdate().String(), "error: ") {
		t.Fatal("String")
	}
	_, d := newBuilderDB(t)
	_ = d
}

func TestUpdateNoReturning(t *testing.T) {
	db, d := newBuilderDB(t)
	d.F &^= dialect.FeatureReturning
	if _, _, err := db.NewUpdate().Table("t").Set("a", 1).Where("x").Returning("id").Build(); !errors.Is(err, ErrReturningUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestDelete(t *testing.T) {
	db, _ := newBuilderDB(t)
	s, args, err := db.NewDelete().With("c", SQL("SELECT 1")).TableAs("users", "u").Where("id = ?", 1).WhereOr("a").
		WhereExpr(SQL("b")).WhereGroup(func(w *WhereBuilder) { w.Where("c") }).Returning("*").Build()
	expectSQL(t, s, args, err, `WITH "c" AS (SELECT 1) DELETE FROM "users" AS "u" WHERE (id = ?) OR (a) AND (b) AND ((c)) RETURNING *`, 1)
	s, args, err = db.NewDelete().Table("t").AllRows().Build()
	expectSQL(t, s, args, err, `DELETE FROM "t"`)
	if _, _, err := db.NewDelete().Table("t").Build(); !errors.Is(err, ErrNoWhere) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := db.NewDelete().Table("t").Where("?").Build(); err == nil {
		t.Fatal("expected error")
	}
	if _, _, err := db.NewDelete().With("c", SQL("?")).Table("t").AllRows().Build(); err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(db.NewDelete().String(), "error: ") {
		t.Fatal("String")
	}
	db2, d := newBuilderDB(t)
	d.F &^= dialect.FeatureReturning
	if _, _, err := db2.NewDelete().Table("t").AllRows().Returning("id").Build(); !errors.Is(err, ErrReturningUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteExecution(t *testing.T) {
	rec := fakedb.New()
	db := NewDB(rec.DB(), testdialect.New())
	ctx := context.Background()
	rec.Returns("RETURNING", []string{"id"}, []driver.Value{int64(7)})

	var id int64
	if err := db.NewInsert().Table("t").Values(1).Returning("id").Scan(ctx, &id); err != nil || id != 7 {
		t.Fatalf("insert scan = %d, %v", id, err)
	}
	if _, err := db.NewInsert().Table("t").Values(1).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.NewInsert().Table("t").Values(1).Returning("id").Rows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err := db.NewUpdate().Table("t").Set("a", 1).Where("x").Returning("id").Scan(ctx, &id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Table("t").Set("a", 1).Where("x").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = db.NewUpdate().Table("t").Set("a", 1).Where("x").Returning("id").Rows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err := db.NewDelete().Table("t").Where("x").Returning("id").Scan(ctx, &id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewDelete().Table("t").Where("x").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = db.NewDelete().Table("t").Where("x").Returning("id").Rows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()

	// errors short-circuit before hitting the database
	n := len(rec.Calls())
	if _, err := db.NewDelete().Table("t").Exec(ctx); err == nil {
		t.Fatal("expected ErrNoWhere")
	}
	if _, err := db.NewDelete().Table("t").Rows(ctx); err == nil {
		t.Fatal("expected ErrNoWhere")
	}
	if err := db.NewDelete().Table("t").Scan(ctx); err == nil {
		t.Fatal("expected ErrNoWhere")
	}
	if len(rec.Calls()) != n {
		t.Fatal("invalid query reached the driver")
	}
}

func TestQueryConstructorsOnTxAndConn(t *testing.T) {
	db, _ := newBuilderDB(t)
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, idb := range []IDB{db, c, tx} {
		if idb.NewSelect() == nil || idb.NewInsert() == nil || idb.NewUpdate() == nil || idb.NewDelete() == nil {
			t.Fatal("constructor returned nil")
		}
	}
}

// Quoted identifiers are cached per dialect: the same name quotes
// differently for each.
func TestWriteIdentPerDialect(t *testing.T) {
	for range 2 { // the second round reads the cache
		for _, tc := range []struct {
			d    dialect.Dialect
			want string
		}{
			{testdialect.Like(dialect.Postgres), `"u"."na""me", "u".*`},
			{testdialect.Like(dialect.MySQL), "`u`.`na\"me`, `u`.*"},
		} {
			w := NewSQLWriter(tc.d)
			w.WriteIdent(`u.na"me`)
			w.WriteString(", ")
			w.WriteIdent("u.*")
			if got := w.String(); got != tc.want {
				t.Fatalf("%s: got %s, want %s", tc.d.Name(), got, tc.want)
			}
		}
	}
}
