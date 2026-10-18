package fakedb

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

func TestRecordsAndReturns(t *testing.T) {
	r := New()
	db := r.DB()
	defer db.Close()
	ctx := context.Background()

	r.Returns("SELECT", []string{"id", "name"}, []driver.Value{int64(1), "a"}, []driver.Value{int64(2), "b"})
	rows, err := db.QueryContext(ctx, "SELECT id, name FROM t WHERE x = ?", 5)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		n++
	}
	rows.Close()
	if n != 2 {
		t.Fatalf("got %d rows", n)
	}
	calls := r.Calls()
	if len(calls) != 1 || calls[0].Args[0] != 5 {
		t.Fatalf("calls = %#v", calls)
	}

	boom := errors.New("boom")
	r.FailOn("DELETE", boom)
	if _, err := db.ExecContext(ctx, "DELETE FROM t"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestReturnsOnce(t *testing.T) {
	r := New()
	db := r.DB()
	r.ReturnsOnce("SELECT", []string{"n"}, []driver.Value{int64(1)})
	r.Returns("SELECT", []string{"n"}, []driver.Value{int64(2)})
	var a, b int64
	if err := db.QueryRow("SELECT n").Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT n").Scan(&b); err != nil {
		t.Fatal(err)
	}
	if a != 1 || b != 2 {
		t.Fatalf("a=%d b=%d", a, b)
	}
}

func TestTxLogging(t *testing.T) {
	r := New()
	db := r.DB()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := r.Queries()
	want := []string{"BEGIN", "INSERT", "COMMIT"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v", got)
	}
	r.Reset()
	if len(r.Calls()) != 0 {
		t.Fatal("reset failed")
	}
}
