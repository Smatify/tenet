// Package dbtest contains the integration test suite every dialect must pass.
// Dialect modules start a real database (SQLite in memory, the others via
// testcontainers) and call Run with a connected *dbal.DB.
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect"
)

// Case is a single integration test.
type Case struct {
	Name string
	Run  func(t *testing.T, db *dbal.DB)
}

var cases []Case

// Register adds cases to the suite. Packages providing new features register
// their integration tests here so every dialect runs them.
func Register(c ...Case) { cases = append(cases, c...) }

// Run executes the whole suite against db.
func Run(t *testing.T, db *dbal.DB) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) { c.Run(t, db) })
	}
}

var tableSeq atomic.Int64

// Table creates a uniquely named table with the given column definitions and
// drops it when the test ends. It returns the unquoted table name.
func Table(t *testing.T, db *dbal.DB, columns string) string {
	t.Helper()
	name := fmt.Sprintf("dbtest_%d", tableSeq.Add(1))
	ctx := context.Background()
	q := db.Dialect().QuoteIdent(name)
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+q)
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+q+" ("+columns+")"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE "+q) })
	return name
}

// Bind replaces each ? in query with the dialect placeholder.
func Bind(d dialect.Dialect, query string) string {
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString(d.Placeholder(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Count returns the number of rows in table.
func Count(t *testing.T, db dbal.IDB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+db.Dialect().QuoteIdent(table)).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func init() {
	Register(
		Case{"Ping", func(t *testing.T, db *dbal.DB) {
			if err := db.PingContext(context.Background()); err != nil {
				t.Fatal(err)
			}
		}},
		Case{"ExecAndQueryWithPlaceholders", testExecQuery},
		Case{"QuotedIdentifiers", testQuotedIdentifiers},
		Case{"StringLiteral", testStringLiteral},
		Case{"TransactionCommitRollback", testTx},
		Case{"NestedTransactions", testNestedTx},
	)
}

func testExecQuery(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	tbl := Table(t, db, "id INT PRIMARY KEY, name VARCHAR(50)")
	ins := Bind(d, "INSERT INTO "+d.QuoteIdent(tbl)+" (id, name) VALUES (?, ?)")
	for i, name := range []string{"a", "b", "c"} {
		if _, err := db.ExecContext(ctx, ins, i+1, name); err != nil {
			t.Fatal(err)
		}
	}
	var name string
	q := Bind(d, "SELECT name FROM "+d.QuoteIdent(tbl)+" WHERE id = ? AND name <> ?")
	if err := db.QueryRowContext(ctx, q, 2, "x").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "b" {
		t.Fatalf("name = %q", name)
	}
	rows, err := db.QueryContext(ctx, "SELECT id FROM "+d.QuoteIdent(tbl)+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[2] != 3 {
		t.Fatalf("ids = %v", ids)
	}
}

func testQuotedIdentifiers(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	col := d.QuoteIdent("select") // reserved word
	tbl := Table(t, db, col+" INT")
	if _, err := db.ExecContext(ctx, "INSERT INTO "+d.QuoteIdent(tbl)+" ("+col+") VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	var v int
	if err := db.QueryRowContext(ctx, "SELECT "+col+" FROM "+d.QuoteIdent(tbl)).Scan(&v); err != nil || v != 1 {
		t.Fatalf("v=%d err=%v", v, err)
	}
}

func testStringLiteral(t *testing.T, db *dbal.DB) {
	in := `it's a "quote" \ backslash`
	var out string
	if err := db.QueryRowContext(context.Background(), "SELECT "+db.Dialect().QuoteString(in)).Scan(&out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("roundtrip: got %q want %q", out, in)
	}
}

func testTx(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	tbl := Table(t, db, "id INT PRIMARY KEY")
	ins := Bind(d, "INSERT INTO "+d.QuoteIdent(tbl)+" (id) VALUES (?)")
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx *dbal.Tx) error {
		_, err := tx.ExecContext(ctx, ins, 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx *dbal.Tx) error {
		if _, err := tx.ExecContext(ctx, ins, 2); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if n := Count(t, db, tbl); n != 1 {
		t.Fatalf("count = %d", n)
	}
}

func testNestedTx(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	d := db.Dialect()
	tbl := Table(t, db, "id INT PRIMARY KEY")
	ins := Bind(d, "INSERT INTO "+d.QuoteIdent(tbl)+" (id) VALUES (?)")
	boom := errors.New("boom")
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx *dbal.Tx) error {
		if _, err := tx.ExecContext(ctx, ins, 1); err != nil {
			return err
		}
		if err := tx.RunInTx(ctx, nil, func(ctx context.Context, inner *dbal.Tx) error {
			_, err := inner.ExecContext(ctx, ins, 2)
			return err
		}); err != nil {
			return err
		}
		err := tx.RunInTx(ctx, nil, func(ctx context.Context, inner *dbal.Tx) error {
			if _, err := inner.ExecContext(ctx, ins, 3); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			return fmt.Errorf("inner: %w", err)
		}
		if n := Count(t, tx, tbl); n != 2 {
			return fmt.Errorf("inside tx count = %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := Count(t, db, tbl); n != 2 {
		t.Fatalf("count = %d", n)
	}
}
