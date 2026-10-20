package orm

import (
	"strings"
	"testing"

	"github.com/smatify/smorm/dialect"
)

func dqlSQL(t *testing.T, em *EntityManager, dql string, params map[string]any) string {
	t.Helper()
	res, err := em.ParseDQL(dql, params)
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := res.Query.SQL()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDQLTranslation(t *testing.T) {
	em, _ := newUnitEM(t, dialect.Postgres)
	_ = em.reg.Register(qAuthor{})
	s := dqlSQL(t, em, `SELECT b FROM qBook b JOIN b.Author a
		WHERE a.Name = :name AND (b.Title LIKE 'Go%' OR b.ID IN (1, 2, ?1)) AND NOT b.Title IS NULL
		ORDER BY a.Name DESC, b.ID`, map[string]any{"name": "ann", "1": 3})
	for _, want := range []string{
		`INNER JOIN "qa" AS "a" ON "a"."id" = "t0"."author_id"`,
		`"a"."name" = $1`, `"t0"."title" LIKE $2`, `"t0"."id" IN ($3, $4, $5)`, `NOT ("t0"."title" IS NULL)`,
		`ORDER BY "a"."name" DESC, "t0"."id" ASC`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	s = dqlSQL(t, em, `select distinct a from qAuthor a left join a.Books b left join b.Author x
		where b.ID between 1 and 5 and a.Name not like 'x%' and a.Name not in :names and a.ID != 0 and a.ID >= -1 and a.ID <> b.ID
		and a.Name is not null and a.Name not between 'a' and 'b' and a.Name = TRUE and a.Name <> FALSE and a.Name = NULL and a.ID < 2.5`,
		map[string]any{"names": []string{"p", "q"}})
	for _, want := range []string{
		`LEFT JOIN "qb" AS "b"`, `LEFT JOIN "qa" AS "x"`, `"b"."id" >= $1`, `NOT ("t0"."name" LIKE`, `"t0"."name" NOT IN (`,
		`"t0"."id" <> $`, `"t0"."id" <> "b"."id"`, `"t0"."name" IS NOT NULL`, `"t0"."name" IS NULL`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	res, err := em.ParseDQL("SELECT COUNT(a) FROM qAuthor a", nil)
	if err != nil || !res.Count {
		t.Fatal("count")
	}
}

func TestDQLErrors(t *testing.T) {
	em, _ := newUnitEM(t, dialect.Postgres)
	_ = em.reg.Register(qAuthor{})
	cases := map[string]string{
		"FROM qAuthor a":                                        "expected SELECT",
		"SELECT a FROM Nope a":                                  "unknown entity",
		"SELECT b FROM qAuthor a":                               "only the root alias",
		"SELECT a FROM qAuthor a JOIN z.Books b":                "unknown alias",
		"SELECT a FROM qAuthor a JOIN a.Nope b":                 "no association",
		"SELECT a FROM qAuthor a WHERE a.Name = :missing":       "missing parameter",
		"SELECT a FROM qAuthor a WHERE a.Name = 'open":          "unterminated",
		"SELECT a FROM qAuthor a WHERE a.Name == 1":             "unknown operator",
		"SELECT a FROM qAuthor a WHERE a.Name # 1":              "unexpected character",
		"SELECT a FROM qAuthor a WHERE a.Name":                  "expected a comparison",
		"SELECT a FROM qAuthor a WHERE a = 1":                   "expected alias.field",
		"SELECT a FROM qAuthor a WHERE a.Name LIKE a.Name":      "LIKE needs a value",
		"SELECT a FROM qAuthor a WHERE a.ID IN (a.ID)":          "IN lists need values",
		"SELECT a FROM qAuthor a WHERE a.ID BETWEEN a.ID AND 2": "BETWEEN needs values",
		"SELECT a FROM qAuthor a ORDER a.Name":                  "expected BY",
		"SELECT a FROM qAuthor a WHERE (a.ID = 1":               "expected \")\"",
		"SELECT a FROM qAuthor a extra":                         "unexpected",
		"SELECT COUNT(a FROM qAuthor a":                         "expected \")\"",
		"SELECT a FROM qAuthor a WHERE a.ID = ?":                "empty parameter",
		"SELECT a FROM qAuthor a WHERE a.ID = ,":                "expected a value",
		"SELECT a FROM qAuthor a WHERE a.ID IS 1":               "expected NULL",
		"SELECT 1 FROM qAuthor a":                               "expected an identifier",
		"SELECT a FROM qAuthor a WHERE a.ID = 1.2.3":            "invalid number",
	}
	for dql, want := range cases {
		if _, err := em.ParseDQL(dql, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", dql, err, want)
		}
	}
	res, err := em.ParseDQL("SELECT a FROM qAuthor a WHERE a.Nope = 1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := res.Query.SQL(); err == nil {
		t.Fatal("unknown field is reported when building")
	}
}
