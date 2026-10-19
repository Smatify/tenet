package dbtest

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smatify/smorm/console"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/migrations"
)

func init() { Register(Case{"Console/MigrationCommands", testConsole}) }

func testConsole(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	r := mapping.NewRegistry()
	must(t, r.Register(mapAuthor{}))
	want := mapping.BuildSchema(r.All())
	DropSchemaQuietly(db, want)
	t.Cleanup(func() { DropSchemaQuietly(db, want) })
	dir := t.TempDir()
	reg := migrations.NewRegistry()
	keep := onlyPrefix("map_")
	app := console.New(console.Config{
		DB: db, Entities: []any{mapAuthor{}}, Registry: r,
		MigrationsDir: dir, MigrationsFormat: migrations.FormatSQL, Migrations: reg,
		IgnoreTables: func(t string) bool { return keep(t) || strings.HasPrefix(t, "mig_") },
	})
	var out, errOut bytes.Buffer
	app.Out, app.Err = &out, &errOut
	run := func(args ...string) int {
		out.Reset()
		errOut.Reset()
		return app.Run(ctx, args)
	}
	// Use the default bookkeeping tables; clean them afterwards.
	t.Cleanup(func() {
		for _, tbl := range migrations.New(db, nil).Tables() {
			_, _ = db.ExecContext(context.Background(), "DROP TABLE "+db.Dialect().QuoteIdent(tbl))
		}
	})
	for _, tbl := range migrations.New(db, nil).Tables() {
		_, _ = db.ExecContext(ctx, "DROP TABLE "+db.Dialect().QuoteIdent(tbl))
	}

	if code := run("schema:validate"); code != 1 || !strings.Contains(out.String(), "not in sync") {
		t.Fatalf("validate before: %d\n%s%s", code, out.String(), errOut.String())
	}
	if code := run("migrations:diff", "--description", "init"); code != 0 || !strings.Contains(out.String(), "Generated new migration") {
		t.Fatalf("diff: %s%s", out.String(), errOut.String())
	}
	must(t, reg.LoadSQL(os.DirFS(dir), "."))
	sqlFile := filepath.Join(dir, "plan.sql")
	if code := run("migrations:migrate", "--write-sql", sqlFile); code != 0 {
		t.Fatal(errOut.String())
	}
	if b, _ := os.ReadFile(sqlFile); !strings.Contains(string(b), "CREATE TABLE") {
		t.Fatalf("write-sql: %s", b)
	}
	if code := run("migrations:migrate", "--dry-run"); code != 0 || !strings.Contains(out.String(), "-> CREATE TABLE") {
		t.Fatalf("dry-run: %s", out.String())
	}
	if code := run("migrations:migrate", "-n"); code != 0 || !strings.Contains(out.String(), "Successfully executed 1") {
		t.Fatalf("migrate: %s%s", out.String(), errOut.String())
	}
	if code := run("migrations:status"); code != 0 || !strings.Contains(out.String(), "migrated") || !strings.Contains(out.String(), "0 pending") {
		t.Fatalf("status: %s", out.String())
	}
	if code := run("schema:validate"); code != 0 || !strings.Contains(out.String(), "in sync") {
		t.Fatalf("validate after: %s%s", out.String(), errOut.String())
	}
	if code := run("migrations:diff"); code != 0 || !strings.Contains(out.String(), "No changes") {
		t.Fatalf("second diff: %s", out.String())
	}
	if code := run("migrations:migrate", "first", "-n"); code != 0 {
		t.Fatal(errOut.String())
	}
	if code := run("schema:update"); code != 0 || !strings.Contains(out.String(), "--force") {
		t.Fatalf("schema:update dump: %s", out.String())
	}
	if code := run("schema:update", "--force"); code != 0 || !strings.Contains(out.String(), "updated successfully") {
		t.Fatalf("schema:update: %s%s", out.String(), errOut.String())
	}
	if code := run("schema:update", "--force"); code != 0 || !strings.Contains(out.String(), "Nothing to update") {
		t.Fatalf("schema:update again: %s", out.String())
	}
	if code := run("schema:validate", "--skip-sync"); code != 0 {
		t.Fatal(out.String())
	}
	versions := reg.All()
	if code := run("migrations:execute", versions[0].Version); code != 1 {
		t.Fatal("execute without direction")
	}
	if code := run("migrations:generate", "--description", "blank"); code != 0 {
		t.Fatal(errOut.String())
	}
	// Interactive cancel.
	app.In = strings.NewReader("no\n")
	if code := run("migrations:migrate"); code != 0 || !strings.Contains(out.String(), "CANCELLED") {
		t.Fatalf("cancel: %s", out.String())
	}
}
