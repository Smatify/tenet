package dbtest

import (
	"context"
	"testing"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
)

func init() { Register(Case{"ORM/DQL", testDQL}) }

func testDQL(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db, colAuthor{}, colComment{})
	em := newEM()
	for _, n := range []string{"ann", "bob", "cid"} {
		a := &colAuthor{Name: n}
		for _, title := range []string{n + "-go", n + "-sql"} {
			a.Posts.Add(&colPost{Title: title, Author: a})
		}
		must(t, em.Persist(a))
	}
	must(t, em.Flush(ctx))
	em = newEM()
	q, err := smorm.DQL[colPost](em, `SELECT p FROM colPost p JOIN p.Author a
		WHERE a.Name IN (:names) OR p.Title LIKE 'cid-%' ORDER BY p.Title DESC`, smorm.Params{"names": "ann"})
	must(t, err)
	posts, err := q.All(ctx)
	must(t, err)
	if len(posts) != 4 || posts[0].Title != "cid-sql" || posts[3].Title != "ann-go" {
		var got []string
		for _, p := range posts {
			got = append(got, p.Title)
		}
		t.Fatalf("posts = %v", got)
	}
	n, err := smorm.DQLCount(ctx, em, "SELECT COUNT(a) FROM colAuthor a JOIN a.Posts p WHERE p.Title LIKE :t", smorm.Params{"t": "%-go"})
	must(t, err)
	if n != 3 {
		t.Fatalf("count = %d", n)
	}
	if _, err := smorm.DQL[colAuthor](em, "SELECT p FROM colPost p", nil); err == nil {
		t.Fatal("type mismatch")
	}
	if _, err := smorm.DQL[colPost](em, "SELECT COUNT(p) FROM colPost p", nil); err == nil {
		t.Fatal("count via DQL")
	}
	if _, err := smorm.DQLCount(ctx, em, "SELECT nonsense", nil); err == nil {
		t.Fatal("parse error")
	}
}
