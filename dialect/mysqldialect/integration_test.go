package mysqldialect

import (
	"context"
	"os"
	"sync"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mariadb"
	"github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dbtest"
	"github.com/smatify/tenet/internal/brand"
)

type server struct {
	once      sync.Once
	container testcontainers.Container
	dsn       string
	err       error
	envKey    string
	start     func(ctx context.Context) (testcontainers.Container, string, error)
}

const dsnParams = "parseTime=true&multiStatements=true"

var (
	mysqlServer = &server{envKey: "mysql_dsn", start: func(ctx context.Context) (testcontainers.Container, string, error) {
		c, err := mysql.Run(ctx, "mysql:8.4", mysql.WithDatabase("test"), mysql.WithUsername("test"), mysql.WithPassword("test"))
		if err != nil {
			return nil, "", err
		}
		dsn, err := c.ConnectionString(ctx, dsnParams)
		return c, dsn, err
	}}
	mariaServer = &server{envKey: "mariadb_dsn", start: func(ctx context.Context) (testcontainers.Container, string, error) {
		c, err := mariadb.Run(ctx, "mariadb:11.4", mariadb.WithDatabase("test"), mariadb.WithUsername("test"), mariadb.WithPassword("test"))
		if err != nil {
			return nil, "", err
		}
		dsn, err := c.ConnectionString(ctx, dsnParams)
		return c, dsn, err
	}}
)

func TestMain(m *testing.M) {
	code := m.Run()
	for _, s := range []*server{mysqlServer, mariaServer} {
		if s.container != nil {
			_ = s.container.Terminate(context.Background())
		}
	}
	os.Exit(code)
}

func (s *server) open(t *testing.T, d *Dialect) *dbal.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	if os.Getenv(brand.Env(s.envKey)) == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}
	s.once.Do(func() {
		if s.dsn = os.Getenv(brand.Env(s.envKey)); s.dsn != "" {
			return
		}
		s.container, s.dsn, s.err = s.start(context.Background())
	})
	if s.err != nil {
		t.Fatalf("start %s: %v", d.Name(), s.err)
	}
	db, err := dbal.Open("mysql", s.dsn, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func openMySQL(t *testing.T) *dbal.DB   { return mysqlServer.open(t, New()) }
func openMariaDB(t *testing.T) *dbal.DB { return mariaServer.open(t, NewMariaDB()) }

func TestIntegrationMySQL(t *testing.T)   { dbtest.Run(t, openMySQL(t)) }
func TestIntegrationMariaDB(t *testing.T) { dbtest.Run(t, openMariaDB(t)) }
