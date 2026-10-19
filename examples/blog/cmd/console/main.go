// Command console is the project console (smorm's bin/console):
//
//	go run ./cmd/console list
package main

import (
	"context"
	"os"

	_ "modernc.org/sqlite"

	"github.com/smatify/smorm/console"
	"github.com/smatify/smorm/dialect/sqlitedialect"
	"github.com/smatify/smorm/gen/maker"
	"github.com/smatify/smorm/migrations"

	_ "example.com/blog/internal/entity"
	_ "example.com/blog/migrations"
)

func main() {
	app := console.New(console.Config{
		Driver:  "sqlite",
		Dialect: sqlitedialect.New(),
		// DSN defaults to $SMORM_DATABASE_URL, e.g.
		// file:app.db?_pragma=foreign_keys(1)
		EntityDir:        "internal/entity",
		EntityImport:     "example.com/blog/internal/entity",
		MigrationsDir:    "migrations",
		MigrationsFormat: migrations.Format("go"),
	})
	maker.Register(app)
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}
