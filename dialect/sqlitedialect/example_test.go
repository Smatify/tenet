package sqlitedialect_test

import (
	"context"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect/sqlitedialect"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/migrations"
	"github.com/smatify/tenet/schema"
)

// Author and Post are entities in the Go style: exported fields, a
// collection on the inverse side, the owning side a pointer.
type Author struct {
	tenet.Entity `tenet:"table:author"`
	ID           int64
	Name         string                 `tenet:"length:100"`
	Posts        tenet.Collection[Post] `tenet:"oneToMany,mappedBy:Author,cascade:persist,orderBy:Title"`
}

type Post struct {
	tenet.Entity `tenet:"table:post"`
	ID           int64
	Title        string  `tenet:"length:200"`
	Views        int     `tenet:"default:0"`
	Author       *Author `tenet:"inversedBy:Posts,notnull"`
}

// open is an SQLite database in memory with the tables of Author and Post
// (an application creates them with migrations) and their registry.
func open(ctx context.Context) (*dbal.DB, *mapping.Registry) {
	db, err := dbal.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)", sqlitedialect.New())
	if err != nil {
		panic(err)
	}
	db.SQL().SetMaxOpenConns(1) // one connection: the database lives in it
	reg := mapping.NewRegistry()
	if err := reg.Register(&Author{}, &Post{}); err != nil {
		panic(err)
	}
	stmts, err := schema.NewDDL(db.Dialect()).CreateSchema(mapping.BuildSchema(reg.All()))
	if err != nil {
		panic(err)
	}
	for _, s := range stmts {
		if _, err := db.SQL().ExecContext(ctx, s); err != nil {
			panic(err)
		}
	}
	return db, reg
}

// Example stores an author with two posts in one flush and queries them
// with a fresh entity manager.
func Example() {
	ctx := context.Background()
	db, reg := open(ctx)
	defer db.Close()

	// One entity manager per request or job.
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx), tenet.WithRegistry(reg))
	ann := &Author{Name: "Ann"}
	for _, title := range []string{"Hello", "Again"} {
		ann.Posts.Add(&Post{Title: title, Author: ann})
	}
	if err := em.Persist(ann); err != nil { // the posts follow (cascade:persist)
		panic(err)
	}
	if err := em.Flush(ctx); err != nil { // one transaction, the author first
		panic(err)
	}

	em = tenet.NewEntityManager(db, tenet.WithContext(ctx), tenet.WithRegistry(reg))
	// Fields are named as in Go (Doctrine style entities: their unexported names).
	posts, err := tenet.NewQuery[Post](em).As("p").Join("p.Author", "a").
		Where(tenet.Eq("a.Name", "Ann")).OrderBy("p.Title", tenet.Asc).With("Author").All(ctx)
	if err != nil {
		panic(err)
	}
	for _, p := range posts {
		fmt.Println(p.Title, "by", p.Author.Name)
	}
	author, err := tenet.Find[Author](ctx, em, ann.ID)
	if err != nil {
		panic(err)
	}
	fmt.Println(author.Name, "wrote", author.Posts.Len(), "posts") // the posts load when used
	// Output:
	// Again by Ann
	// Hello by Ann
	// Ann wrote 2 posts
}

// Example_changes writes only what changed: Flush updates the one field
// set, Remove deletes, and a bulk update changes many rows in one
// statement without loading them.
func Example_changes() {
	ctx := context.Background()
	db, reg := open(ctx)
	defer db.Close()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx), tenet.WithRegistry(reg))
	ann := &Author{Name: "Ann"}
	hello, again := &Post{Title: "Hello", Author: ann}, &Post{Title: "Again", Author: ann}
	for _, e := range []any{ann, hello, again} {
		if err := em.Persist(e); err != nil {
			panic(err)
		}
	}
	if err := em.Flush(ctx); err != nil {
		panic(err)
	}

	hello.Title = "Hello, world" // a loaded entity: changed, written on the next flush
	if err := em.Remove(again); err != nil {
		panic(err)
	}
	if err := em.Flush(ctx); err != nil {
		panic(err)
	}
	n, err := tenet.Update[Post](em).Set("Views", tenet.Raw("{Views} + 10")).Where(tenet.Eq("Author", ann.ID)).Exec(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(n, "post updated")

	type line struct {
		Title string
		Views int
	}
	lines, err := tenet.Scan[line](ctx, tenet.NewQuery[Post](em).Columns("{Title} AS title", "{Views} AS views"))
	if err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", lines)
	// Output:
	// 1 post updated
	// [{Title:Hello, world Views:10}]
}

// Example_transactional runs work and its flush in one transaction: an
// error rolls everything back.
func Example_transactional() {
	ctx := context.Background()
	db, reg := open(ctx)
	defer db.Close()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx), tenet.WithRegistry(reg))
	err := em.Transactional(ctx, func(ctx context.Context, em *tenet.EntityManager) error {
		if err := em.Persist(&Author{Name: "Ann"}); err != nil {
			return err
		}
		return fmt.Errorf("something went wrong")
	})
	fmt.Println(err)
	n, _ := tenet.NewQuery[Author](tenet.NewEntityManager(db, tenet.WithRegistry(reg))).Count(ctx)
	fmt.Println(n, "authors")
	// Output:
	// something went wrong
	// 0 authors
}

// Example_migrations applies versioned migrations, each in its own
// transaction; the migrator keeps which ran.
func Example_migrations() {
	ctx := context.Background()
	db, err := dbal.Open("sqlite", "file::memory:", sqlitedialect.New())
	if err != nil {
		panic(err)
	}
	defer db.Close()
	db.SQL().SetMaxOpenConns(1)

	reg := migrations.NewRegistry() // generated migrations call migrations.Register
	if err := reg.Add(migrations.Migration{
		Version:     "20260101120000",
		Description: "the author table",
		Up: func(ctx context.Context, m *migrations.M) error {
			m.AddSQL("CREATE TABLE author (id INTEGER PRIMARY KEY, name VARCHAR(100) NOT NULL)")
			return nil
		},
		Down: func(ctx context.Context, m *migrations.M) error {
			m.AddSQL("DROP TABLE author")
			return nil
		},
	}); err != nil {
		panic(err)
	}
	mig := migrations.New(db, reg)
	steps, err := mig.Migrate(ctx, "") // "": the latest
	if err != nil {
		panic(err)
	}
	for _, s := range steps {
		fmt.Println("applied", s.Migration.Version, s.Migration.Description)
	}
	steps, _ = mig.Migrate(ctx, "")
	fmt.Println(len(steps), "to apply now")
	// Output:
	// applied 20260101120000 the author table
	// 0 to apply now
}

// Example_reading filters, pages and sums: conditions on a joined entity,
// a page with the total, and a projection read into a struct.
func Example_reading() {
	ctx := context.Background()
	db, reg := open(ctx)
	defer db.Close()
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx), tenet.WithRegistry(reg))
	ann, bob := &Author{Name: "Ann"}, &Author{Name: "Bob"}
	for _, a := range []*Author{ann, bob} {
		if err := em.Persist(a); err != nil {
			panic(err)
		}
	}
	for i, a := range []*Author{ann, ann, ann, bob} {
		if err := em.Persist(&Post{Title: fmt.Sprint("Post ", i), Views: 10 * i, Author: a}); err != nil {
			panic(err)
		}
	}
	if err := em.Flush(ctx); err != nil {
		panic(err)
	}

	posts, err := tenet.NewQuery[Post](em).As("p").Join("p.Author", "a").
		Where(tenet.Like("a.Name", "A%"), tenet.Gt("p.Views", 5)).OrderBy("p.Title", tenet.Asc).All(ctx)
	if err != nil {
		panic(err)
	}
	for _, p := range posts {
		fmt.Println(p.Title, p.Views)
	}

	page, err := tenet.Paginate(ctx, tenet.NewQuery[Post](em).OrderBy("Title", tenet.Asc), 2, 3)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(page.Items), "of", page.Total, "more:", page.HasNext())

	type views struct {
		Author int64
		Total  int
	}
	rows, err := tenet.Scan[views](ctx, tenet.NewQuery[Post](em).
		Columns("{Author} AS author", "sum({Views}) AS total").GroupBy("{Author}").OrderBy("Author", tenet.Asc))
	if err != nil {
		panic(err)
	}
	fmt.Println(rows[0].Author == ann.ID, rows[0].Total, rows[1].Total)
	// Output:
	// Post 1 10
	// Post 2 20
	// 1 of 4 more: false
	// true 30 30
}
