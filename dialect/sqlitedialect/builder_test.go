package sqlitedialect

import (
	"testing"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/internal/fakedb"
)

func TestBuilderSQL(t *testing.T) {
	db := dbal.NewDB(fakedb.New().DB(), New())
	defer db.Close()
	s, _, err := db.NewSelect().Table("t").Offset(5).ForUpdate().Build()
	if err != nil || s != `SELECT * FROM "t" LIMIT -1 OFFSET 5` {
		t.Fatalf("got %s, %v", s, err)
	}
}
