package mssqldialect

import (
	"testing"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/internal/fakedb"
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
	s, a, err := db.NewSelect().TableAs("t", "x").Where("a = ? AND b = ?", 1, 2).Limit(10).Offset(5).ForUpdate().SkipLocked().NoWait().Build()
	check(t, s, a, err, "SELECT * FROM [t] AS [x] WITH (UPDLOCK, ROWLOCK, READPAST, NOWAIT) WHERE (a = @p1 AND b = @p2) ORDER BY (SELECT NULL) OFFSET 5 ROWS FETCH NEXT 10 ROWS ONLY")
	s, a, err = db.NewSelect().Table("t").Lock(dbal.LockForShare).OrderBy("id", dbal.Asc).Limit(1).Build()
	check(t, s, a, err, "SELECT * FROM [t] WITH (HOLDLOCK, ROWLOCK) ORDER BY [id] ASC OFFSET 0 ROWS FETCH NEXT 1 ROWS ONLY")
	s, a, err = db.NewSelect().WithRecursive("r", dbal.SQL("SELECT 1 AS n")).Table("r").Offset(2).Build()
	check(t, s, a, err, "WITH [r] AS (SELECT 1 AS n) SELECT * FROM [r] ORDER BY (SELECT NULL) OFFSET 2 ROWS")
	s, a, err = db.NewInsert().Table("t").Columns("k", "v").Values(1, 2).Returning("id").Build()
	check(t, s, a, err, "INSERT INTO [t] ([k], [v]) OUTPUT INSERTED.[id] VALUES (@p1, @p2)")
	s, a, err = db.NewInsert().Table("t").Columns("k", "v").Values(1, 2).Values(3, 4).OnConflict("k").DoUpdate("v").Returning("id").Build()
	check(t, s, a, err, "MERGE INTO [t] WITH (HOLDLOCK) AS target USING (VALUES (@p1, @p2), (@p3, @p4)) AS source ([k], [v]) ON (target.[k] = source.[k]) "+
		"WHEN MATCHED THEN UPDATE SET target.[v] = source.[v] WHEN NOT MATCHED THEN INSERT ([k], [v]) VALUES (source.[k], source.[v]) OUTPUT INSERTED.[id];")
	s, a, err = db.NewInsert().Table("t").Columns("k").Values(1).OnConflict("k").DoNothing().Build()
	check(t, s, a, err, "MERGE INTO [t] WITH (HOLDLOCK) AS target USING (VALUES (@p1)) AS source ([k]) ON (target.[k] = source.[k]) WHEN NOT MATCHED THEN INSERT ([k]) VALUES (source.[k]);")
	s, a, err = db.NewUpdate().TableAs("t", "x").Set("v", 1).Where("x.k = ?", 2).Returning("v").Build()
	check(t, s, a, err, "UPDATE [x] SET [v] = @p1 OUTPUT INSERTED.[v] FROM [t] AS [x] WHERE (x.k = @p2)")
	s, a, err = db.NewUpdate().Table("t").Set("v", 1).Where("k = 2").Build()
	check(t, s, a, err, "UPDATE [t] SET [v] = @p1 WHERE (k = 2)")
	s, a, err = db.NewDelete().Table("t").Where("k = 1").Returning("k").Build()
	check(t, s, a, err, "DELETE FROM [t] OUTPUT DELETED.[k] WHERE (k = 1)")
	s, a, err = db.NewDelete().TableAs("t", "x").Where("x.k = 1").Returning("k").Build()
	check(t, s, a, err, "DELETE [x] OUTPUT DELETED.[k] FROM [t] AS [x] WHERE (x.k = 1)")
}

func TestMergeErrors(t *testing.T) {
	db := builderDB(t)
	cases := []*dbal.InsertQuery{
		db.NewInsert().Table("t").Columns("k").Values(1).DoNothing(),
		db.NewInsert().Table("t").Values(1).OnConflict("k").DoNothing(),
		db.NewInsert().Table("t").Columns("k").Values(dbal.SQL("?")).OnConflict("k").DoNothing(),
		db.NewInsert().With("c", dbal.SQL("?")).Table("t").Columns("k").Values(1).OnConflict("k").DoNothing(),
		db.NewInsert().Table("t").Columns("k").Values(1).OnConflict("k").DoUpdateSet("k", "?"),
	}
	for i, q := range cases {
		if _, _, err := q.Build(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}
