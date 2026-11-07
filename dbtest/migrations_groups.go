package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/migrations"
	"github.com/smatify/tenet/schema"
	"github.com/smatify/tenet/types"
)

func init() { Register(Case{"Migrations/GroupsRollback", testMigrationGroups}) }

func testMigrationGroups(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	q := db.Dialect().QuoteIdent
	// A versions table from before group support is upgraded transparently.
	for _, tbl := range []string{"mig_versions", "mig_locks"} {
		_, _ = db.ExecContext(ctx, "DROP TABLE "+q(tbl))
	}
	reg := migrations.NewRegistry()
	must(t, reg.Add(migrations.Migration{Version: "001", Up: createTableMigration("mig_a"), Down: dropTable("mig_a")}))
	must(t, reg.Add(migrations.Migration{Version: "002", Up: createTableMigration("mig_b"), Down: dropTable("mig_b")}))
	must(t, reg.Add(migrations.Migration{Version: "003", Up: createTableMigration("mig_c"), Down: dropTable("mig_c")}))
	m := newMigrator(t, db, reg)
	legacy := schema.New().CreateTable("mig_versions")
	legacy.AddColumn("version", types.String).SetLength(191)
	legacy.AddColumn("executed_at", types.DateTime).SetNullable(true)
	legacy.AddColumn("execution_time", types.Integer).SetNullable(true)
	legacy.SetPrimaryKey("version")
	stmts, err := schema.NewDDL(db.Dialect()).CreateTable(legacy)
	must(t, err)
	ApplySchema(t, db, stmts)
	must(t, m.Init(ctx))

	if _, err := m.Rollback(ctx, false); err == nil {
		t.Fatal("nothing to roll back")
	}
	_, err = m.Migrate(ctx, "002") // group 1: 001, 002
	must(t, err)
	_, err = m.Migrate(ctx, "") // group 2: 003
	must(t, err)
	last, err := m.LastGroup(ctx)
	must(t, err)
	if strings.Join(last, ",") != "003" {
		t.Fatalf("last group = %v", last)
	}
	dry, err := m.Rollback(ctx, true)
	must(t, err)
	if len(dry) != 1 || len(dry[0].SQL) != 1 {
		t.Fatalf("dry rollback = %+v", dry)
	}
	steps, err := m.Rollback(ctx, false)
	must(t, err)
	if len(steps) != 1 || steps[0].Migration.Version != "003" {
		t.Fatalf("rollback = %+v", steps)
	}
	// Group 1 is reverted newest first.
	steps, err = m.Rollback(ctx, false)
	must(t, err)
	if len(steps) != 2 || steps[0].Migration.Version != "002" || steps[1].Migration.Version != "001" {
		t.Fatalf("second rollback = %+v", steps)
	}
	st, err := m.Status(ctx)
	must(t, err)
	for _, s := range st {
		if s.Applied {
			t.Fatalf("still applied: %+v", s)
		}
	}
	// Execute creates its own group.
	_, err = m.Execute(ctx, "003", true)
	must(t, err)
	if last, _ := m.LastGroup(ctx); strings.Join(last, ",") != "003" {
		t.Fatalf("execute group = %v", last)
	}
	_, err = m.Rollback(ctx, false)
	must(t, err)
}
