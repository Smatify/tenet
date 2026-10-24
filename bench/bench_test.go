// Package bench compares smorm with GORM, bun, ent and sqlc on Postgres
// (started with testcontainers unless $<PREFIX>PG_DSN is set):
//
//	GOWORK=off go test -run '^$' -bench . -benchmem
package bench

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/smatify/smorm/internal/brand"
)

// impl is one library under test. Every function works on a fresh unit of
// work where the library has one.
type impl struct {
	name string
	// insert stores n posts of author 1.
	insert func(ctx context.Context, n int) error
	// find loads the post with the identifier.
	find func(ctx context.Context, id int64) (title string, err error)
	// load100 loads the first 100 posts with their authors (preloaded).
	load100 func(ctx context.Context) (authors []string, err error)
	// update100 loads the first 100 posts and increments their views.
	update100 func(ctx context.Context) error
}

var (
	impls     []impl
	once      sync.Once
	container testcontainers.Container
	db        *sql.DB
	setupErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if container != nil {
		_ = container.Terminate(context.Background())
	}
	os.Exit(code)
}

// open connects once and returns the shared pool.
func open(tb testing.TB) *sql.DB {
	tb.Helper()
	if testing.Short() {
		tb.Skip("needs Postgres")
	}
	once.Do(func() {
		ctx := context.Background()
		dsn := os.Getenv(brand.Env("pg_dsn"))
		if dsn == "" {
			var c *postgres.PostgresContainer
			c, setupErr = postgres.Run(ctx, "postgres:17-alpine", postgres.WithDatabase("bench"),
				postgres.WithUsername("bench"), postgres.WithPassword("bench"), postgres.BasicWaitStrategies())
			if setupErr != nil {
				return
			}
			container = c
			if dsn, setupErr = c.ConnectionString(ctx, "sslmode=disable"); setupErr != nil {
				return
			}
		}
		if db, setupErr = sql.Open("pgx", dsn); setupErr != nil {
			return
		}
		schema, err := os.ReadFile("schema.sql")
		if err != nil {
			setupErr = err
			return
		}
		_, setupErr = db.ExecContext(ctx, "DROP TABLE IF EXISTS post, author; "+string(schema))
		if setupErr == nil {
			setupErr = setupLibraries(db)
		}
	})
	if setupErr != nil {
		tb.Fatal(setupErr)
	}
	return db
}

// reset leaves 100 authors and 1000 posts (post i belongs to author i%100+1).
func reset(tb testing.TB) {
	tb.Helper()
	db := open(tb)
	_, err := db.Exec(`TRUNCATE post, author RESTART IDENTITY;
		INSERT INTO author (name) SELECT 'author ' || i FROM generate_series(1, 100) i;
		INSERT INTO post (author_id, title, views) SELECT i % 100 + 1, 'post ' || i, 0 FROM generate_series(1, 1000) i`)
	if err != nil {
		tb.Fatal(err)
	}
}

func setupLibraries(db *sql.DB) error {
	for _, s := range setups {
		if err := s(db); err != nil {
			return err
		}
	}
	return nil
}

// setups are run once with the pool before the first benchmark.
var setups []func(*sql.DB) error

// TestImplementations checks that every library does what is measured.
func TestImplementations(t *testing.T) {
	ctx := context.Background()
	for _, im := range impls {
		t.Run(im.name, func(t *testing.T) {
			reset(t)
			if err := im.insert(ctx, 3); err != nil {
				t.Fatal(err)
			}
			if err := im.insert(ctx, 1); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := db.QueryRow("SELECT COUNT(*) FROM post WHERE author_id = 1").Scan(&n); err != nil || n != 14 {
				t.Fatalf("posts of author 1 = %d, %v", n, err)
			}
			if title, err := im.find(ctx, 7); err != nil || title != "post 7" {
				t.Fatalf("find = %q, %v", title, err)
			}
			authors, err := im.load100(ctx)
			if err != nil || len(authors) != 100 || authors[0] != "author 2" || authors[99] != "author 1" {
				t.Fatalf("load100 = %d authors, %v", len(authors), err)
			}
			if err := im.update100(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM post WHERE views = 1").Scan(&n); err != nil || n != 100 {
				t.Fatalf("updated = %d, %v", n, err)
			}
		})
	}
}

func BenchmarkInsert(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{1, 100, 1000} {
		for _, im := range impls {
			b.Run(fmt.Sprintf("%d/%s", n, im.name), func(b *testing.B) {
				reset(b)
				for b.Loop() {
					if err := im.insert(ctx, n); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkFindByID(b *testing.B) {
	ctx := context.Background()
	for _, im := range impls {
		b.Run(im.name, func(b *testing.B) {
			reset(b)
			var i int64
			for b.Loop() {
				if _, err := im.find(ctx, i%1000+1); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})
	}
}

func BenchmarkLoad100WithAuthor(b *testing.B) {
	ctx := context.Background()
	for _, im := range impls {
		b.Run(im.name, func(b *testing.B) {
			reset(b)
			for b.Loop() {
				if _, err := im.load100(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkUpdate100(b *testing.B) {
	ctx := context.Background()
	for _, im := range impls {
		b.Run(im.name, func(b *testing.B) {
			reset(b)
			for b.Loop() {
				if err := im.update100(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
