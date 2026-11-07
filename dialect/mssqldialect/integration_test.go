package mssqldialect

import (
	"context"
	"os"
	"sync"
	"testing"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mssql"

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

// openTestDB connects to $<PREFIX>MSSQL_DSN or starts a SQL Server container.
func openTestDB(t *testing.T) *dbal.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	if os.Getenv(brand.Env("mssql_dsn")) == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}
	once.Do(func() {
		if dsn = os.Getenv(brand.Env("mssql_dsn")); dsn != "" {
			return
		}
		ctx := context.Background()
		var c *mssql.MSSQLServerContainer
		c, setupErr = mssql.Run(ctx, "mcr.microsoft.com/mssql/server:2022-latest",
			mssql.WithAcceptEULA(), mssql.WithPassword("Str0ng!Passw0rd"))
		if setupErr != nil {
			return
		}
		container = c
		dsn, setupErr = c.ConnectionString(ctx)
	})
	if setupErr != nil {
		t.Fatalf("start mssql: %v", setupErr)
	}
	db, err := dbal.Open("sqlserver", dsn, New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestIntegration(t *testing.T) { dbtest.Run(t, openTestDB(t)) }
