package dbtest

import (
	"context"
	"errors"
	"os"
	"testing"
	"testing/fstest"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/migrations"
	"github.com/smatify/tenet/schema"
	"github.com/smatify/tenet/types"
	"github.com/smatify/tenet/validate"
)

func init() {
	Register(
		Case{"Migrations/Lifecycle", testMigrations},
		Case{"Migrations/DiffGenerateMigrate", testMigrationsDiff},
	)
}

func newMigrator(t *testing.T, db *dbal.DB, reg *migrations.Registry) *migrations.Migrator {
	t.Helper()
	m := migrations.New(db, reg, migrations.WithTable("mig_versions"), migrations.WithLockTable("mig_locks"))
	cleanup := func() {
		for _, tbl := range []string{"mig_c", "mig_b", "mig_a", "mig_versions", "mig_locks"} {
			_, _ = db.ExecContext(context.Background(), "DROP TABLE "+db.Dialect().QuoteIdent(tbl))
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	return m
}

func createTableMigration(name string) func(context.Context, *migrations.M) error {
	return func(_ context.Context, m *migrations.M) error {
		s := schema.New()
		tbl := s.CreateTable(name)
		tbl.AddColumn("id", types.Integer)
		tbl.SetPrimaryKey("id")
		stmts, err := schema.NewDDL(m.Dialect()).CreateTable(tbl)
		for _, q := range stmts {
			m.AddSQL(q)
		}
		return err
	}
}

func dropTable(name string) func(context.Context, *migrations.M) error {
	return func(_ context.Context, m *migrations.M) error {
		m.AddSQL("DROP TABLE " + m.Dialect().QuoteIdent(name))
		return nil
	}
}

func testMigrations(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	reg := migrations.NewRegistry()
	must(t, reg.Add(migrations.Migration{Version: "001", Description: "a", Up: createTableMigration("mig_a"), Down: dropTable("mig_a")}))
	must(t, reg.Add(migrations.Migration{Version: "002", Description: "data",
		Up: func(ctx context.Context, m *migrations.M) error {
			if m.DB() == nil {
				return nil // dry run
			}
			_, err := m.DB().NewInsert().Table("mig_a").Columns("id").Values(1).Values(2).Exec(ctx)
			return err
		},
		Down: func(ctx context.Context, m *migrations.M) error {
			m.AddSQL("DELETE FROM " + m.Dialect().QuoteIdent("mig_a"))
			return nil
		}}))
	q := db.Dialect().QuoteIdent
	must(t, reg.LoadSQL(fstest.MapFS{
		"sql/003_b.up.sql":   {Data: []byte("CREATE TABLE " + q("mig_b") + " (id INT PRIMARY KEY)\n" + migrations.SplitMarker + "\nINSERT INTO " + q("mig_b") + " (id) VALUES (7)\n")},
		"sql/003_b.down.sql": {Data: []byte("DROP TABLE " + q("mig_b"))},
	}, "sql"))
	m := newMigrator(t, db, reg)

	dry, err := m.DryRun(ctx, "latest")
	must(t, err)
	if len(dry) != 3 || len(dry[0].SQL) == 0 || len(dry[2].SQL) != 2 {
		t.Fatalf("dry run = %+v", dry)
	}
	if st, _ := m.Status(ctx); st[0].Applied {
		t.Fatal("dry run must not apply")
	}

	steps, err := m.Migrate(ctx, "")
	must(t, err)
	if len(steps) != 3 || Count(t, db, "mig_a") != 2 || Count(t, db, "mig_b") != 1 {
		t.Fatalf("migrate: %d steps", len(steps))
	}
	st, err := m.Status(ctx)
	must(t, err)
	for _, s := range st {
		if !s.Applied || s.ExecutedAt.IsZero() || s.Unknown {
			t.Fatalf("status = %+v", s)
		}
	}
	if steps, _ := m.Migrate(ctx, "latest"); len(steps) != 0 {
		t.Fatal("second run must be a no-op")
	}

	steps, err = m.Migrate(ctx, "prev")
	must(t, err)
	if len(steps) != 1 || steps[0].Up || steps[0].Migration.Version != "003" {
		t.Fatalf("prev = %+v", steps)
	}
	plan, err := m.Plan(ctx, "next")
	must(t, err)
	if len(plan) != 1 || plan[0].Migration.Version != "003" {
		t.Fatalf("next plan = %+v", plan)
	}
	_, err = m.Execute(ctx, "003", true)
	must(t, err)
	if _, err := m.Execute(ctx, "003", true); err == nil {
		t.Fatal("executing an applied migration must fail")
	}
	_, err = m.Execute(ctx, "003", false)
	must(t, err)
	if _, err := m.Execute(ctx, "999", true); err == nil {
		t.Fatal("unknown version")
	}

	// Lock contention.
	must(t, m.Lock(ctx))
	if _, err := m.Migrate(ctx, ""); !errors.Is(err, migrations.ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	must(t, m.Unlock(ctx))

	// A failing migration stops the run and is not recorded.
	must(t, reg.Add(migrations.Migration{Version: "004", Up: func(_ context.Context, m *migrations.M) error {
		m.AddSQL("INSERT INTO " + m.Dialect().QuoteIdent("mig_a") + " (id) VALUES (3)")
		m.AddSQL("THIS IS NOT SQL")
		return nil
	}}))
	steps, err = m.Migrate(ctx, "")
	if err == nil || len(steps) != 1 {
		t.Fatalf("expected failure after one step, got %d steps, err %v", len(steps), err)
	}
	if Count(t, db, "mig_a") != 2 {
		t.Fatal("the failing migration's transaction must be rolled back")
	}
	st, _ = m.Status(ctx)
	if st[len(st)-1].Applied {
		t.Fatal("failed migration recorded")
	}
	if _, err := m.Execute(ctx, "004", false); err == nil {
		t.Fatal("reverting an unapplied migration")
	}
	if _, err := m.Plan(ctx, "nope"); err == nil {
		t.Fatal("unknown target")
	}

	// Revert everything; 004 has no Down but is not applied.
	steps, err = m.Migrate(ctx, "first")
	must(t, err)
	if len(steps) != 3 {
		t.Fatalf("first = %d steps", len(steps))
	}
	if _, err := m.Migrate(ctx, "prev"); err == nil {
		t.Fatal("nothing to roll back")
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM "+q("mig_a")); err == nil {
		t.Fatal("mig_a must be dropped")
	}
	// Irreversible migrations cannot be reverted.
	reg2 := migrations.NewRegistry()
	must(t, reg2.Add(migrations.Migration{Version: "010", Up: createTableMigration("mig_c")}))
	m2 := newMigrator(t, db, reg2)
	_, err = m2.Migrate(ctx, "")
	must(t, err)
	if _, err := m2.Migrate(ctx, "first"); !errors.Is(err, migrations.ErrIrreversible) {
		t.Fatalf("irreversible = %v", err)
	}
	// Unknown versions in the database are reported.
	m3 := migrations.New(db, migrations.NewRegistry(), migrations.WithTable("mig_versions"), migrations.WithLockTable("mig_locks"))
	st, err = m3.Status(ctx)
	must(t, err)
	if len(st) != 1 || !st[0].Unknown {
		t.Fatalf("unknown = %+v", st)
	}
	if !m3.IsOwnTable("MIG_VERSIONS") || len(m3.Tables()) != 2 {
		t.Fatal("own tables")
	}
}

func testMigrationsDiff(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	r := mapping.NewRegistry()
	must(t, r.Register(mapAuthor{}))
	entities := r.All()
	want := mapping.BuildSchema(entities)
	DropSchemaQuietly(db, want)
	t.Cleanup(func() { DropSchemaQuietly(db, want) })
	m := newMigrator(t, db, migrations.NewRegistry())
	must(t, m.Init(ctx))
	ignore := onlyPrefix("map_")
	up, down, err := m.Diff(ctx, entities, ignore)
	must(t, err)
	if len(up) == 0 || len(down) == 0 {
		t.Fatalf("diff up=%d down=%d", len(up), len(down))
	}
	dir := t.TempDir()
	_, err = migrations.Generate(migrations.GenerateOptions{Dir: dir, Format: migrations.FormatSQL, Version: "20260101000000", Description: "init", Up: up, Down: down})
	must(t, err)
	reg := migrations.NewRegistry()
	must(t, reg.LoadSQL(os.DirFS(dir), "."))
	m = migrations.New(db, reg, migrations.WithTable("mig_versions"), migrations.WithLockTable("mig_locks"))
	_, err = m.Migrate(ctx, "")
	must(t, err)
	rep, err := validate.Run(ctx, validate.Options{Entities: entities, DB: db, Ignore: func(t string) bool { return ignore(t) || m.IsOwnTable(t) }})
	must(t, err)
	if !rep.OK() {
		t.Fatalf("not in sync after migrating:\n%s", rep)
	}
	if up, _, err := m.Diff(ctx, entities, ignore); err != nil || len(up) != 0 {
		t.Fatalf("second diff = %v %v", up, err)
	}
	_, err = m.Migrate(ctx, "first")
	must(t, err)
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM "+db.Dialect().QuoteIdent("map_author")); err == nil {
		t.Fatal("down migration must drop the tables")
	}
}
