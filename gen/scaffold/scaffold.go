// Package scaffold creates the project files for a new tenet setup
// (cmd/console, the entity package and the migrations package).
package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"text/template"

	"github.com/smatify/tenet/internal/brand"
)

// Driver describes how to open a database with a dialect.
type Driver struct {
	Dialect      string // dialect package name, e.g. pgdialect
	DialectNew   string // constructor, e.g. New
	DriverImport string
	DriverName   string
	ExampleDSN   string
}

// Drivers maps dialect names accepted by init to their setup.
var Drivers = map[string]Driver{
	"postgres": {"pgdialect", "New", "github.com/jackc/pgx/v5/stdlib", "pgx", "postgres://user:pass@localhost:5432/app?sslmode=disable"},
	"mysql":    {"mysqldialect", "New", "github.com/go-sql-driver/mysql", "mysql", "user:pass@tcp(localhost:3306)/app?parseTime=true&multiStatements=true"},
	"mariadb":  {"mysqldialect", "NewMariaDB", "github.com/go-sql-driver/mysql", "mysql", "user:pass@tcp(localhost:3306)/app?parseTime=true&multiStatements=true"},
	"sqlite":   {"sqlitedialect", "New", "modernc.org/sqlite", "sqlite", "file:app.db?_pragma=foreign_keys(1)"},
	"mssql":    {"mssqldialect", "New", "github.com/microsoft/go-mssqldb", "sqlserver", "sqlserver://sa:pass@localhost:1433?database=app"},
}

// Options configures Init.
type Options struct {
	Dir           string // project root containing go.mod
	Dialect       string // key of Drivers
	EntityDir     string // default internal/entity
	MigrationsDir string // default migrations
	Format        string // go or sql
}

var moduleRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// ModulePath reads the module path from dir/go.mod.
func ModulePath(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("scaffold: %w (run go mod init first)", err)
	}
	m := moduleRe.FindSubmatch(b)
	if m == nil {
		return "", errors.New("scaffold: go.mod has no module line")
	}
	return string(m[1]), nil
}

// Init writes the scaffold and returns the created files. Existing files
// are never overwritten.
func Init(o Options) ([]string, error) {
	if o.EntityDir == "" {
		o.EntityDir = "internal/entity"
	}
	if o.MigrationsDir == "" {
		o.MigrationsDir = "migrations"
	}
	if o.Format == "" {
		o.Format = "go"
	}
	drv, ok := Drivers[o.Dialect]
	if !ok {
		return nil, fmt.Errorf("scaffold: unknown dialect %q (postgres, mysql, mariadb, sqlite, mssql)", o.Dialect)
	}
	mod, err := ModulePath(o.Dir)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"Module": mod, "Root": "github.com/smatify/" + brand.Name, "Name": brand.Name, "Driver": drv,
		"EntityDir": filepath.ToSlash(o.EntityDir), "MigrationsDir": filepath.ToSlash(o.MigrationsDir),
		"Format": o.Format, "Env": brand.Env("database_url"),
	}
	files := map[string]*template.Template{
		"cmd/console/main.go":                           mainTmpl,
		filepath.Join(o.EntityDir, "doc.go"):            entityTmpl,
		filepath.Join(o.MigrationsDir, "migrations.go"): migrationsTmpl,
	}
	var created []string
	for _, name := range []string{"cmd/console/main.go", filepath.Join(o.EntityDir, "doc.go"), filepath.Join(o.MigrationsDir, "migrations.go")} {
		path := filepath.Join(o.Dir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		var buf bytes.Buffer
		if err := files[name].Execute(&buf, data); err != nil {
			return nil, err
		}
		src, err := format.Source(buf.Bytes())
		if err != nil {
			return nil, fmt.Errorf("scaffold: %s: %w", name, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			return nil, err
		}
		created = append(created, path)
	}
	return created, nil
}

var mainTmpl = template.Must(template.New("main").Parse(`// Command console is the project console ({{.Name}}'s bin/console):
//
//	go run ./cmd/console list
package main

import (
	"context"
	"os"

	_ "{{.Driver.DriverImport}}"

	"{{.Root}}/console"
	"{{.Root}}/dialect/{{.Driver.Dialect}}"
	"{{.Root}}/gen/maker"
	"{{.Root}}/migrations"

	_ "{{.Module}}/{{.EntityDir}}"
	_ "{{.Module}}/{{.MigrationsDir}}"
)

func main() {
	app := console.New(console.Config{
		Driver:  "{{.Driver.DriverName}}",
		Dialect: {{.Driver.Dialect}}.{{.Driver.DialectNew}}(),
		// DSN defaults to ${{.Env}}, e.g.
		// {{.Driver.ExampleDSN}}
		EntityDir:        "{{.EntityDir}}",
		EntityImport:     "{{.Module}}/{{.EntityDir}}",
		MigrationsDir:    "{{.MigrationsDir}}",
		MigrationsFormat: migrations.Format("{{.Format}}"),
	})
	maker.Register(app)
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}
`))

var entityTmpl = template.Must(template.New("entity").Parse(`// Package entity contains the entities of the application.
// make:entity creates and updates them; regenerate accessors with go generate.
package entity

//go:generate go run {{.Root}}/gen/cmd/generate
`))

var migrationsTmpl = template.Must(template.New("migrations").Parse(`// Package migrations contains the versioned database migrations.
// Create them with "go run ./cmd/console make:migration".
package migrations
`))
