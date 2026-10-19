package migrations

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func noop(context.Context, *M) error { return nil }

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(Migration{Version: "2", Up: noop}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Migration{Version: "1", Up: noop}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Migration{Version: "1", Up: noop}); err == nil {
		t.Fatal("duplicate")
	}
	if err := r.Add(Migration{Version: "bad version", Up: noop}); err == nil {
		t.Fatal("invalid version")
	}
	if err := r.Add(Migration{Version: "3"}); err == nil {
		t.Fatal("missing Up")
	}
	all := r.All()
	if len(all) != 2 || all[0].Version != "1" {
		t.Fatal("sorted")
	}
	if _, ok := r.Get("2"); !ok {
		t.Fatal("Get")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Register must panic on invalid migrations")
		}
	}()
	Register(Migration{Version: "x"})
}

func TestSplitSQL(t *testing.T) {
	script := "CREATE TABLE a (id INT);\n" + SplitMarker + "\n-- only a comment\n" + SplitMarker + "\nINSERT INTO a VALUES (1);\n"
	got := SplitSQL(script)
	if !slices.Equal(got, []string{"CREATE TABLE a (id INT);", "INSERT INTO a VALUES (1);"}) {
		t.Fatalf("got %q", got)
	}
	if SplitSQL("  \n") != nil {
		t.Fatal("empty")
	}
}

func TestLoadSQL(t *testing.T) {
	fsys := fstest.MapFS{
		"m/001_create_users.up.sql":   {Data: []byte("CREATE TABLE u (id INT)")},
		"m/001_create_users.down.sql": {Data: []byte("DROP TABLE u")},
		"m/002.up.sql":                {Data: []byte(NoTxMarker + "\nCREATE INDEX i ON u (id)")},
		"m/readme.txt":                {Data: []byte("ignored")},
	}
	r := NewRegistry()
	if err := r.LoadSQL(fsys, "m"); err != nil {
		t.Fatal(err)
	}
	m1, _ := r.Get("001")
	m2, _ := r.Get("002")
	if m1.Description != "create users" || m1.Down == nil || m1.NoTransaction || !m2.NoTransaction || m2.Down != nil {
		t.Fatalf("m1=%+v m2=%+v", m1, m2)
	}
	mm := &M{}
	_ = m1.Up(context.Background(), mm)
	if !slices.Equal(mm.Statements(), []string{"CREATE TABLE u (id INT)"}) || mm.DB() != nil || mm.Dialect() != nil {
		t.Fatal("sql func")
	}
	mm.Skip("x")
	if mm.skip != "x" {
		t.Fatal("skip")
	}
	bad := fstest.MapFS{"m/003.down.sql": {Data: []byte("x")}}
	if err := NewRegistry().LoadSQL(bad, "m"); err == nil {
		t.Fatal("down without up")
	}
	if err := NewRegistry().LoadSQL(fsys, "missing"); err == nil {
		t.Fatal("missing dir")
	}
	dup := fstest.MapFS{"m/001.up.sql": {Data: []byte("x")}}
	r2 := NewRegistry()
	_ = r2.Add(Migration{Version: "001", Up: noop})
	if err := r2.LoadSQL(dup, "m"); err == nil {
		t.Fatal("duplicate version")
	}
}

func TestGenerate(t *testing.T) {
	dir := t.TempDir()
	files, err := Generate(GenerateOptions{Dir: dir, Version: "20261019000000", Description: "Add `users` table!",
		Up: []string{"CREATE TABLE `users` (id INT)", "PRAGMA foreign_keys = OFF"}, Down: []string{"DROP TABLE \"users\""}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(files[0]) != "20261019000000_add_users_table.go" {
		t.Fatalf("file = %s", files[0])
	}
	src, _ := os.ReadFile(files[0])
	if _, err := parser.ParseFile(token.NewFileSet(), files[0], src, 0); err != nil {
		t.Fatalf("generated Go does not parse: %v\n%s", err, src)
	}
	for _, s := range []string{"package migrations", `"CREATE TABLE ` + "`users`" + ` (id INT)"`, "NoTransaction: true", `"DROP TABLE \"users\""`} {
		if !strings.Contains(string(src), s) {
			t.Errorf("missing %q in\n%s", s, src)
		}
	}
	files, err = Generate(GenerateOptions{Dir: dir, Format: FormatSQL, Version: "20261019000001", Up: []string{"A", "B"}})
	if err != nil {
		t.Fatal(err)
	}
	up, _ := os.ReadFile(files[0])
	down, _ := os.ReadFile(files[1])
	if string(up) != "A\n"+SplitMarker+"\nB\n" || !strings.Contains(string(down), "nothing to do") || filepath.Base(files[0]) != "20261019000001.up.sql" {
		t.Fatalf("up=%q down=%q", up, down)
	}
	files, err = Generate(GenerateOptions{Dir: dir, Format: FormatSQL, Description: "x", Up: []string{"PRAGMA foreign_keys = ON"}})
	if err != nil || !strings.HasPrefix(readFile(t, files[0]), NoTxMarker) {
		t.Fatal("no-tx marker")
	}
	if _, err := Generate(GenerateOptions{Dir: dir, Format: "yaml"}); err == nil {
		t.Fatal("unknown format")
	}
	if _, err := Generate(GenerateOptions{Dir: "/dev/null/x"}); err == nil {
		t.Fatal("bad dir")
	}
	if NewVersion(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) != "20260102030405" {
		t.Fatal("NewVersion")
	}
}

func readFile(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
