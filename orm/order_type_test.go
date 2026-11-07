package orm

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/tenet/dialect"
	"github.com/smatify/tenet/internal/entity"
)

type uShelf struct {
	entity.Header `tenet:"table:shelf"`
	ID            string `tenet:"generated:none"`
}

type uItem struct {
	entity.Header `tenet:"table:item"`
	ID            string  `tenet:"generated:none"`
	Shelf         *uShelf `tenet:"notnull"`
}

// TestInsertsGroupedByType: parents and children persisted in turns are
// written one statement per type, parents first.
func TestInsertsGroupedByType(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.Postgres)
	s1, s2 := &uShelf{ID: "s1"}, &uShelf{ID: "s2"}
	for _, e := range []any{s1, &uItem{ID: "i1", Shelf: s1}, s2, &uItem{ID: "i2", Shelf: s2}, &uItem{ID: "i3", Shelf: s1}} {
		if err := em.Persist(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := em.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var inserts []string
	for _, q := range rec.Queries() {
		if strings.HasPrefix(q, "INSERT") {
			inserts = append(inserts, q)
		}
	}
	if len(inserts) != 2 || !strings.HasPrefix(inserts[0], `INSERT INTO "shelf"`) || !strings.Contains(inserts[1], `($3, $4), ($5, $6)`) {
		t.Fatalf("inserts:\n%s", strings.Join(inserts, "\n"))
	}
}
