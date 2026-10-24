// Command smorm sets up projects and regenerates entity code:
//
//	smorm init --dialect postgres
//	smorm generate [--accessors [--entity A,B]] [dir...]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/smatify/smorm/gen"
	"github.com/smatify/smorm/gen/maker"
	"github.com/smatify/smorm/gen/rename"
	"github.com/smatify/smorm/gen/scaffold"
	"github.com/smatify/smorm/internal/brand"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(errOut, "usage: %s init --dialect <postgres|mysql|mariadb|sqlite|mssql> | generate [--accessors [--entity A,B]] [dir...] | rename-tags --to <key> [dir]\n", brand.CLIName)
		return 2
	}
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		fs.SetOutput(errOut)
		o := scaffold.Options{}
		fs.StringVar(&o.Dir, "dir", ".", "project root (containing go.mod)")
		fs.StringVar(&o.Dialect, "dialect", "postgres", "database: postgres, mysql, mariadb, sqlite or mssql")
		fs.StringVar(&o.EntityDir, "entities", "internal/entity", "entity package directory")
		fs.StringVar(&o.MigrationsDir, "migrations", "migrations", "migrations directory")
		fs.StringVar(&o.Format, "format", "go", "migration format: go or sql")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		files, err := scaffold.Init(o)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		for _, f := range files {
			fmt.Fprintln(out, " created:", f)
		}
		fmt.Fprintf(out, "\nNext steps:\n  go mod tidy\n  export %s=...\n  go run ./cmd/console make:entity\n", brand.Env("database_url"))
		return 0
	case "rename-tags":
		fs := flag.NewFlagSet("rename-tags", flag.ContinueOnError)
		fs.SetOutput(errOut)
		from := fs.String("from", brand.TagKey, "current struct tag key")
		to := fs.String("to", "", "new struct tag key")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		dir := fs.Arg(0)
		if dir == "" {
			dir = "."
		}
		files, err := rename.Tags(dir, *from, *to)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		for _, f := range files {
			fmt.Fprintln(out, " updated:", f)
		}
		fmt.Fprintf(out, "%d file(s) updated\n", len(files))
		return 0
	case "generate":
		fs := flag.NewFlagSet("generate", flag.ContinueOnError)
		fs.SetOutput(errOut)
		accessors := fs.Bool("accessors", false, "also write missing getters/setters of mapped fields added by hand")
		entities := fs.String("entity", "", "with --accessors: only these entities (comma separated)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		dirs := fs.Args()
		if len(dirs) == 0 {
			dirs = []string{"."}
		}
		for _, d := range dirs {
			if *accessors {
				added, err := (&maker.Maker{Dir: d}).Accessors(strings.FieldsFunc(*entities, func(r rune) bool { return r == ',' })...)
				if err != nil {
					fmt.Fprintln(errOut, err)
					return 1
				}
				for _, m := range added {
					fmt.Fprintln(out, " added:", m)
				}
			}
			f, err := gen.Generate(d)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			if f != "" {
				fmt.Fprintln(out, " generated:", f)
			}
		}
		return 0
	}
	fmt.Fprintf(errOut, "unknown command %q\n", args[0])
	return 2
}
