// Command console is the project console (tenet's bin/console):
//
//	go run ./cmd/console list
package main

import (
	"context"
	"os"

	_ "modernc.org/sqlite"

	"github.com/smatify/tenet/console"
	"github.com/smatify/tenet/dialect/sqlitedialect"
	"github.com/smatify/tenet/gen/maker"
	"github.com/smatify/tenet/migrations"

	_ "example.com/blog/internal/entity"
	_ "example.com/blog/migrations"
)

func main() {
	app := console.New(console.Config{
		Driver:  "sqlite",
		Dialect: sqlitedialect.New(),
		// DSN defaults to $TENET_DATABASE_URL, e.g.
		// file:app.db?_pragma=foreign_keys(1)
		EntityDir:        "internal/entity",
		EntityImport:     "example.com/blog/internal/entity",
		MigrationsDir:    "migrations",
		MigrationsFormat: migrations.Format("go"),
	})
	maker.Register(app)
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}
