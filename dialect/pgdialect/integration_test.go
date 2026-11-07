package pgdialect

import (
	"context"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dbtest"
	"github.com/smatify/tenet/internal/brand"
)

var (
	once      sync.Once
	container testcontainers.Container
	dsn       string
	setupErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if container != nil {
		_ = container.Terminate(context.Background())
	}
	os.Exit(code)
}

// openTestDB connects to $<PREFIX>PG_DSN or starts a Postgres container.
func openTestDB(t *testing.T) *dbal.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	if os.Getenv(brand.Env("pg_dsn")) == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}
	once.Do(func() {
		if dsn = os.Getenv(brand.Env("pg_dsn")); dsn != "" {
			return
		}
		ctx := context.Background()
		var c *postgres.PostgresContainer
		c, setupErr = postgres.Run(ctx, "postgres:17-alpine",
			postgres.WithDatabase("test"), postgres.WithUsername("test"), postgres.WithPassword("test"),
			postgres.BasicWaitStrategies())
		if setupErr != nil {
			return
		}
		container = c
		dsn, setupErr = c.ConnectionString(ctx, "sslmode=disable")
	})
	if setupErr != nil {
		t.Fatalf("start postgres: %v", setupErr)
	}
	db, err := dbal.Open("pgx", dsn, New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestIntegration(t *testing.T) { dbtest.Run(t, openTestDB(t)) }
