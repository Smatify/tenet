package sqlitedialect

import (
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dbtest"
)

// openTestDB opens a fresh file-backed SQLite database.
func openTestDB(t *testing.T) *dbal.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := dbal.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate", New())
	if err != nil {
		t.Fatal(err)
	}
	db.SQL().SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestIntegration(t *testing.T) { dbtest.Run(t, openTestDB(t)) }
