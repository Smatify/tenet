package dbtest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect"
)

func init() { Register(Case{"ORM/LongInList", testORMLongIn}) }

// testORMLongIn queries with lists longer than the bind parameter limit:
// one array parameter where the dialect has them, an error before sending
// elsewhere.
func testORMLongIn(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, colAuthor{})
	em := newEM()
	for _, n := range []string{"ann", "bob", "cid"} {
		must(t, em.Persist(&colAuthor{Name: n}))
	}
	must(t, em.Flush(ctx))

	limit := dialect.MaxBindParams(db.Dialect())
	ids := make([]int64, limit+10)
	names := make([]string, limit+10)
	for i := range ids {
		ids[i] = -int64(i) - 1
		names[i] = fmt.Sprint("x", i)
	}
	all, err := tenet.NewQuery[colAuthor](newEM()).Where(tenet.In("Name", []string{"ann", "bob"})).OrderBy("Name", tenet.Asc).All(ctx)
	must(t, err)
	ids = append(ids, all[0].ID, all[1].ID)
	names = append(names, "ann", "cid")

	em = newEM()
	got, err := tenet.NewQuery[colAuthor](em).
		Where(tenet.In("ID", ids), tenet.NotIn("Name", names[:len(names)-2])).
		OrderBy("Name", tenet.Asc).All(ctx)
	if !db.Dialect().Features().Has(dialect.FeatureArrayParams) {
		if err == nil || !strings.Contains(err.Error(), "bind parameters") {
			t.Fatalf("long IN list without array parameters: %v", err)
		}
		return
	}
	must(t, err)
	if !slices.Equal(names2(got), []string{"ann", "bob"}) {
		t.Fatalf("IN by ID = %v", names2(got))
	}
	byName, err := tenet.RepositoryFor[colAuthor](em).FindBy(ctx, tenet.Criteria{"Name": names}, tenet.OrderBy("Name", tenet.Asc))
	must(t, err)
	if !slices.Equal(names2(byName), []string{"ann", "cid"}) {
		t.Fatalf("Criteria IN by name = %v", names2(byName))
	}
}
