package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
)

func init() { Register(Case{"ORM/OneToOneInverse", testORMOneToOne}) }

type ooUser struct {
	tenet.Entity `tenet:"table:oo_user"`
	ID           int64
	Name         string     `tenet:"length:50"`
	Profile      *ooProfile `tenet:"oneToOne,mappedBy:User"`
}

type ooProfile struct {
	tenet.Entity `tenet:"table:oo_profile"`
	ID           int64
	Bio          string  `tenet:"length:100"`
	User         *ooUser `tenet:"oneToOne,inversedBy:Profile,notnull,onDelete:cascade"`
}

func testORMOneToOne(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, ooUser{})
	em := newEM()
	var users []*ooUser
	for i, n := range []string{"a", "b", "c"} {
		u := &ooUser{Name: n}
		must(t, em.Persist(u))
		if i < 2 {
			must(t, em.Persist(&ooProfile{Bio: "bio " + n, User: u}))
		}
		users = append(users, u)
	}
	must(t, em.Flush(ctx))

	profileQueries := 0
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "SELECT" && strings.Contains(ev.Query, "oo_profile") {
			profileQueries++
		}
	}})
	em2 := newEM()
	all, err := tenet.NewQuery[ooUser](em2).OrderBy("Name", tenet.Asc).All(ctx)
	must(t, err)
	if profileQueries != 1 {
		t.Fatalf("inverse oneToOne must load in one batched query, got %d", profileQueries)
	}
	if all[0].Profile == nil || all[0].Profile.Bio != "bio a" || all[1].Profile.User != all[1] || all[2].Profile != nil {
		t.Fatalf("profiles = %+v %+v %+v", all[0].Profile, all[1].Profile, all[2].Profile)
	}
	u, err := tenet.Find[ooUser](ctx, newEM(), users[1].ID)
	must(t, err)
	if u.Profile == nil || u.Profile.Bio != "bio b" {
		t.Fatal("Find must load the inverse side")
	}
}
