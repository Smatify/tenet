package dbtest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
)

func init() { Register(Case{"ORM/ZeroIdentifier", testORMZeroID}) }

type zParent struct {
	tenet.Entity `tenet:"table:z_parent"`
	ID           int64  `tenet:"id,generated:none"`
	Name         string `tenet:"length:50"`
}

type zChild struct {
	tenet.Entity `tenet:"table:z_child"`
	ID           int64
	Parent       *zParent `tenet:"manyToOne"`
}

// testORMZeroID covers rows whose identifier is 0: a value like any other
// once the row is stored, not "no identifier yet".
func testORMZeroID(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, zParent{}, zChild{})
	q := db.Dialect().QuoteIdent
	if _, err := db.ExecContext(ctx, Bind(db.Dialect(), "INSERT INTO "+q("z_parent")+" ("+q("id")+", "+q("name")+") VALUES (?, ?)"), 0, "zero"); err != nil {
		t.Fatal(err)
	}

	em := newEM()
	parent, err := tenet.Find[zParent](ctx, em, int64(0))
	must(t, err)
	if again, err := tenet.Find[zParent](ctx, em, int64(0)); err != nil || again != parent {
		t.Fatalf("second Find of ID 0: %p (%v), want the managed %p", again, err, parent)
	}

	child := &zChild{Parent: parent}
	must(t, em.Persist(child))
	must(t, em.Flush(ctx))

	var parentID sql.NullInt64
	must(t, db.QueryRowContext(ctx, Bind(db.Dialect(), "SELECT "+q("parent_id")+" FROM "+q("z_child")+" WHERE "+q("id")+" = ?"), child.ID).Scan(&parentID))
	if !parentID.Valid || parentID.Int64 != 0 {
		t.Fatalf("parent_id = %v, want 0", parentID)
	}

	loaded, err := tenet.Find[zChild](ctx, newEM(), child.ID)
	must(t, err)
	if loaded.Parent == nil {
		t.Fatal("reloaded child lost its parent with ID 0")
	}
	must(t, tenet.Load(ctx, loaded.Parent))
	if loaded.Parent.ID != 0 || loaded.Parent.Name != "zero" {
		t.Fatalf("reloaded parent = %+v, want ID 0 named zero", *loaded.Parent)
	}
}
