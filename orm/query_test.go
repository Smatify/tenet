package orm

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/mapping"
)

type qAuthor struct {
	entity.Header `smorm:"table:qa"`
	ID            int64
	Name          string
	Books         Collection[qBook] `smorm:"oneToMany,mappedBy:Author"`
	Tags          Collection[qTag]  `smorm:"manyToMany"`
	Profile       *qProfile         `smorm:"oneToOne,mappedBy:Author"`
}

type qBook struct {
	entity.Header `smorm:"table:qb"`
	ID            int64
	Title         string
	Author        *qAuthor `smorm:"inversedBy:Books"`
}

type qTag struct {
	entity.Header `smorm:"table:qt"`
	ID            int64
	Label         string
}

type qProfile struct {
	entity.Header `smorm:"table:qp"`
	ID            int64
	Author        *qAuthor `smorm:"oneToOne,inversedBy:Profile"`
}

func queryEM(t *testing.T) *EntityManager {
	em, _ := newUnitEM(t, dialect.Postgres)
	if err := em.reg.Register(qAuthor{}); err != nil {
		t.Fatal(err)
	}
	return em
}

func sqlOf(t *testing.T, q *Query) string {
	t.Helper()
	s, _, err := q.SQL()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestQuerySQL(t *testing.T) {
	em := queryEM(t)
	q := em.NewQuery(typeOf[qBook]()).Join("Author", "a").Where(Eq("a.Name", "x"), Gt("ID", 1)).OrderBy("a.Name", dbal.Desc).Limit(5).Offset(10)
	want := `SELECT "t0"."id", "t0"."title", "t0"."author_id" FROM "qb" AS "t0" INNER JOIN "qa" AS "a" ON "a"."id" = "t0"."author_id" ` +
		`WHERE (("a"."name" = $1) AND ("t0"."id" > $2)) ORDER BY "a"."name" DESC LIMIT 5 OFFSET 10`
	if got := sqlOf(t, q); got != want {
		t.Fatalf("\n got: %s\nwant: %s", got, want)
	}
	if q.Meta().Name != "qBook" {
		t.Fatal("Meta")
	}
	wrapped := sqlOf(t, em.NewQuery(typeOf[qAuthor]()).LeftJoin("Books", "b").Where(Like("b.Title", "a%")).OrderBy("Name", dbal.Asc))
	if !strings.Contains(wrapped, `WHERE ("t0"."id" IN (SELECT "t0"."id" FROM "qa" AS "t0" LEFT JOIN "qb" AS "b" ON "b"."author_id" = "t0"."id"`) {
		t.Fatalf("collection join must be wrapped: %s", wrapped)
	}
	m2m := sqlOf(t, em.NewQuery(typeOf[qAuthor]()).Join("Tags", "tg"))
	if !strings.Contains(m2m, `INNER JOIN "qa_qt" AS "tg_jt" ON "tg_jt"."q_author_id" = "t0"."id" INNER JOIN "qt" AS "tg" ON "tg"."id" = "tg_jt"."q_tag_id"`) {
		t.Fatalf("m2m join: %s", m2m)
	}
	inv := sqlOf(t, em.NewQuery(typeOf[qAuthor]()).Join("Profile", "p").Where(IsNull("p.ID"), IsNotNull("Name"), Neq("Name", "a"),
		Gte("ID", 1), Lt("ID", 9), Lte("ID", 8), NotIn("ID", []int{3}), Or(), Not(Raw("{p.ID} > ?", 0))).ForUpdate())
	for _, s := range []string{`INNER JOIN "qp" AS "p" ON "p"."author_id" = "t0"."id"`, `"p"."id" IS NULL`, `"t0"."name" IS NOT NULL`,
		`NOT IN ($`, `(1 = 1)`, `NOT ("p"."id" > $`, `FOR UPDATE`} {
		if !strings.Contains(inv, s) {
			t.Errorf("missing %q in %s", s, inv)
		}
	}
	// FieldRef resolution prefers the root and detects ambiguity.
	ref := mapping.FieldRef{Entity: "qAuthor", Name: "Name"}
	if s := sqlOf(t, em.NewQuery(typeOf[qAuthor]()).Join("Books", "b").Join("b.Author", "a2").Where(Eq(ref, "x"))); !strings.Contains(s, `"t0"."name" = $1`) {
		t.Fatalf("root preferred: %s", s)
	}
	if _, _, err := em.NewQuery(typeOf[qBook]()).Join("Author", "a").Join("a.Books", "b2").Join("b2.Author", "a3").Where(Eq(ref, "x")).SQL(); err == nil {
		t.Fatal("ambiguous FieldRef")
	}
	// Join takes FieldRefs of the root or of a joined entity.
	viaRef := em.NewQuery(typeOf[qBook]()).Join(mapping.FieldRef{Entity: "qBook", Name: "Author"}, "a").
		Join(mapping.FieldRef{Entity: "qAuthor", Name: "Tags"}, "tg")
	if s := sqlOf(t, viaRef); !strings.Contains(s, `INNER JOIN "qa" AS "a" ON "a"."id" = "t0"."author_id"`) || !strings.Contains(s, `"tg_jt"."q_author_id" = "a"."id"`) {
		t.Fatalf("join via FieldRef: %s", s)
	}
	if w := em.NewQuery(typeOf[qBook]()).With(mapping.FieldRef{Entity: "qBook", Name: "Author"}, "Author.Tags"); w.err != nil || strings.Join(w.with, ",") != "Author,Author.Tags" {
		t.Fatalf("With: %v %v", w.with, w.err)
	}
	c := em.NewQuery(typeOf[qAuthor]()).Where(Eq("Name", "x"))
	d := c.Clone().Where(Eq("ID", 1))
	if sqlOf(t, c) == sqlOf(t, d) {
		t.Fatal("Clone must be independent")
	}
}

func TestQueryErrors(t *testing.T) {
	ctx := context.Background()
	em := queryEM(t)
	author := &qAuthor{}
	cases := []*Query{
		em.NewQuery(typeOf[int]()),
		em.NewQuery(typeOf[qAuthor]()).Join("x.Books", "b"),
		em.NewQuery(typeOf[qAuthor]()).Join("Nope", "b"),
		em.NewQuery(typeOf[qAuthor]()).Join("Books", RootAlias),
		em.NewQuery(typeOf[qAuthor]()).Join("Books", ""),
		em.NewQuery(typeOf[qAuthor]()).Where(Eq("zz.Name", 1)),
		em.NewQuery(typeOf[qAuthor]()).Where(Eq(42, 1)),
		em.NewQuery(typeOf[qAuthor]()).Where(Eq(mapping.FieldRef{Entity: "Other", Name: "x"}, 1)),
		em.NewQuery(typeOf[qAuthor]()).Where(In("ID", 5)),
		em.NewQuery(typeOf[qAuthor]()).Where(In("Nope", []int{1})),
		em.NewQuery(typeOf[qBook]()).Where(Eq("Author", &qTag{})),
		em.NewQuery(typeOf[qBook]()).Where(In("Author", []any{&qTag{}})),
		em.NewQuery(typeOf[qAuthor]()).Where(Not(Eq("Nope", 1))),
		em.NewQuery(typeOf[qAuthor]()).Where(Raw("{Nope} = 1")),
		em.NewQuery(typeOf[qAuthor]()).Where(And(Eq("Nope", 1))),
		em.NewQuery(typeOf[qAuthor]()).OrderBy("Nope", dbal.Asc),
		em.NewQuery(typeOf[qAuthor]()).Join(42, "b"),
		em.NewQuery(typeOf[qAuthor]()).Join(mapping.FieldRef{Entity: "qBook", Name: "Author"}, "a"),
		em.NewQuery(typeOf[qAuthor]()).With(mapping.FieldRef{Entity: "qBook", Name: "Author"}),
		em.NewQuery(typeOf[qAuthor]()).With(42),
		em.NewQuery(typeOf[int]()).With("Books"),
	}
	for i, q := range cases {
		if _, _, err := q.SQL(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if _, err := em.NewQuery(typeOf[qAuthor]()).Where(Eq("Nope", 1)).Count(ctx); err == nil {
		t.Fatal("Count error")
	}
	if _, err := em.NewQuery(typeOf[qAuthor]()).Where(Eq("Nope", 1)).All(ctx); err == nil {
		t.Fatal("All error")
	}
	if _, err := em.NewQuery(typeOf[qAuthor]()).Where(Eq("Nope", 1)).One(ctx); err == nil {
		t.Fatal("One error")
	}
	for _, err := range em.NewQuery(typeOf[qAuthor]()).Where(Eq("Nope", 1)).Iterate(ctx) {
		if err == nil {
			t.Fatal("Iterate error")
		}
	}
	if s := sqlOf(t, em.NewQuery(typeOf[qBook]()).Where(Eq("Author", author))); !strings.Contains(s, `"t0"."author_id" = $1`) {
		t.Fatal(s)
	}
	em.closed = true
	if _, err := em.NewQuery(typeOf[qAuthor]()).All(ctx); err != ErrClosed {
		t.Fatal("closed")
	}
}

func TestQueryExecution(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.Postgres)
	_ = em.reg.Register(qAuthor{})
	rec.Returns("COUNT", []string{"n"}, []driver.Value{int64(3)})
	n, err := em.NewQuery(typeOf[qAuthor]()).Count(ctx)
	if err != nil || n != 3 {
		t.Fatalf("count = %d %v", n, err)
	}
	if ok, _ := em.NewQuery(typeOf[qAuthor]()).Exists(ctx); !ok {
		t.Fatal("exists")
	}
	if _, err := em.NewQuery(typeOf[qAuthor]()).One(ctx); err != ErrNotFound {
		t.Fatalf("one = %v", err)
	}
	cnt := 0
	for range em.NewQuery(typeOf[qAuthor]()).Iterate(ctx) {
		cnt++
	}
	if cnt != 0 {
		t.Fatal("no rows")
	}
}

func TestRawExpandsSlicesAfterIn(t *testing.T) {
	em := queryEM(t)
	for in, want := range map[string]string{
		`{ID} IN ? AND {Name} = ?`:         `"t0"."id" IN ($1, $2) AND "t0"."name" = $3`,
		`{ID} in (?) AND {Name} = ?`:       `"t0"."id" in ($1, $2) AND "t0"."name" = $3`,
		`{ID} NOT IN ( ? ) AND {Name} = ?`: `"t0"."id" NOT IN ($1, $2) AND "t0"."name" = $3`,
		`{ID} = ANY(?) AND {Name} = ?`:     `"t0"."id" = ANY($1) AND "t0"."name" = $2`,
		`'IN ?' = ? AND {Name} = ?`:        `'IN ?' = $1 AND "t0"."name" = $2`,
	} {
		s, args, err := em.NewQuery(typeOf[qAuthor]()).Where(Raw(in, []int64{1, 2}, "x")).SQL()
		if err != nil || !strings.Contains(s, want) {
			t.Errorf("%s:\n got: %s %v\nwant: %s", in, s, err, want)
		}
		if strings.Contains(want, "ANY") || strings.HasPrefix(in, "'") {
			if len(args) != 2 {
				t.Errorf("%s: args = %v", in, args)
			}
		}
	}
	s, _, _ := em.NewQuery(typeOf[qAuthor]()).Where(Raw(`{ID} IN ?`, []int64{})).SQL()
	if !strings.Contains(s, `IN (NULL)`) {
		t.Fatal(s)
	}
}
