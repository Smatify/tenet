package dbtest

import (
	"context"
	"testing"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
)

func init() { Register(Case{"ORM/UniqueHeader", testORMUniqueHeader}) }

type uqUser struct {
	tenet.Entity `tenet:"table:uq_user"`
	ID           int64
	Name         string `tenet:"length:50"`
}

// uqVisit keeps one row per user and page: a unique index over a join
// column and a field, declared on the header.
type uqVisit struct {
	tenet.Entity `tenet:"table:uq_visit,unique:User|Page"`
	ID           int64
	User         *uqUser `tenet:"manyToOne,notnull"`
	Page         string  `tenet:"length:50"`
}

func testORMUniqueHeader(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, uqUser{}, uqVisit{})

	em := newEM()
	ann, bob := &uqUser{Name: "ann"}, &uqUser{Name: "bob"}
	must(t, em.Persist(ann))
	must(t, em.Persist(bob))
	for _, v := range []*uqVisit{{User: ann, Page: "home"}, {User: ann, Page: "about"}, {User: bob, Page: "home"}} {
		must(t, em.Persist(v))
	}
	must(t, em.Flush(ctx))

	dup := newEM()
	user, err := tenet.Find[uqUser](ctx, dup, ann.ID)
	must(t, err)
	must(t, dup.Persist(&uqVisit{User: user, Page: "home"}))
	if err := dup.Flush(ctx); err == nil {
		t.Fatal("a second visit of the same user and page was stored")
	}

	n, err := tenet.NewQuery[uqVisit](newEM()).Count(ctx)
	must(t, err)
	if n != 3 {
		t.Fatalf("%d visits, want 3", n)
	}
}
