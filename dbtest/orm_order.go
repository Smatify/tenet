package dbtest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/schema"
)

func init() { Register(Case{"ORM/OrderNulls", testORMOrderNulls}) }

type ordMatch struct {
	tenet.Entity `tenet:"table:ord_match,index:StartsAt desc nulls last|ID;Name desc"`
	ID           int64
	Name         string `tenet:"length:20"`
	StartsAt     *time.Time
}

// testORMOrderNulls places NULL values first or last and keeps index
// columns with their order through creation and introspection.
func testORMOrderNulls(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, ordMatch{})
	got, err := schema.Introspect(ctx, db, db.Dialect())
	must(t, err)
	reg := mapping.NewRegistry()
	must(t, reg.Register(ordMatch{}))
	want := mapping.BuildSchema(reg.All())
	if diff := schema.NewComparator(db.Dialect()).IgnoreTables(onlyPrefix("ord_")).Compare(got, want); !diff.IsEmpty() {
		t.Fatalf("index order not kept:\n%s", DescribeDiff(diff))
	}

	em := newEM()
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, m := range []*ordMatch{{Name: "a", StartsAt: &t1}, {Name: "b"}, {Name: "c", StartsAt: new(t1.Add(time.Hour))}} {
		must(t, em.Persist(m))
	}
	must(t, em.Flush(ctx))
	for o, want := range map[tenet.Order][]string{
		tenet.Desc.NullsLast():  {"c", "a", "b"},
		tenet.Desc.NullsFirst(): {"b", "c", "a"},
		tenet.Asc.NullsLast():   {"a", "c", "b"},
		tenet.Asc.NullsFirst():  {"b", "a", "c"},
	} {
		ms, err := tenet.NewQuery[ordMatch](newEM()).OrderBy("StartsAt", o).All(ctx)
		must(t, err)
		var names []string
		for _, m := range ms {
			names = append(names, m.Name)
		}
		if !slices.Equal(names, want) {
			t.Errorf("%s: %v, want %v", o, names, want)
		}
		rows, err := tenet.Scan[string](ctx, tenet.NewQuery[ordMatch](newEM()).Columns("{Name}").
			OrderBy(tenet.Raw("{StartsAt}"), o))
		must(t, err)
		if !slices.Equal(rows, want) {
			t.Errorf("projection %s: %v, want %v", o, rows, want)
		}
	}
}
