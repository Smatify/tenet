package console

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/smatify/smorm/migrations"
	"github.com/smatify/smorm/validate"
)

func builtinCommands() []Command {
	return []Command{migrateCmd(), statusCmd(), executeCmd(), generateCmd(), diffCmd(), validateCmd(), updateCmd()}
}

// ignore combines the configured filter with the migrator's own tables.
func (a *App) ignore(m *migrations.Migrator) func(string) bool {
	return func(t string) bool {
		return (m != nil && m.IsOwnTable(t)) || (a.Config.IgnoreTables != nil && a.Config.IgnoreTables(t))
	}
}

func printSteps(c *Ctx, steps []migrations.Step, dry bool) {
	for _, s := range steps {
		verb := "++ migrating"
		if !s.Up {
			verb = "-- reverting"
		}
		c.Printf("  %s %s %s\n", verb, s.Migration.Version, s.Migration.Description)
		if s.Skipped != "" {
			c.Printf("     skipped: %s\n", s.Skipped)
		}
		if dry {
			for _, q := range s.SQL {
				c.Printf("     -> %s\n", q)
			}
		} else if s.Duration > 0 {
			c.Printf("     (%s)\n", s.Duration.Round(time.Millisecond))
		}
	}
}

func writeSQL(path string, steps []migrations.Step) error {
	var b strings.Builder
	for _, s := range steps {
		dir := "up"
		if !s.Up {
			dir = "down"
		}
		fmt.Fprintf(&b, "-- Version %s (%s)\n", s.Migration.Version, dir)
		for _, q := range s.SQL {
			b.WriteString(q + ";\n")
		}
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func migrateCmd() Command {
	var dry *bool
	var sqlFile *string
	return Command{
		Name:        "migrations:migrate",
		Usage:       "[version|latest|first|prev|next]",
		Description: "Execute migrations up or down to a version (default: latest)",
		Flags: func(fs *flag.FlagSet) {
			dry = fs.Bool("dry-run", false, "show the SQL without executing it")
			sqlFile = fs.String("write-sql", "", "write the SQL to this file instead of executing it")
		},
		Run: func(ctx context.Context, c *Ctx) error {
			m, err := c.App.Migrator()
			if err != nil {
				return err
			}
			target := c.Arg(0)
			if *dry || *sqlFile != "" {
				steps, err := m.DryRun(ctx, target)
				if err != nil {
					return err
				}
				if *sqlFile != "" {
					if err := writeSQL(*sqlFile, steps); err != nil {
						return err
					}
					c.Success(fmt.Sprintf("SQL written to %s", *sqlFile))
					return nil
				}
				printSteps(c, steps, true)
				return nil
			}
			if c.Interactive && !c.Confirm("WARNING! You are about to execute migrations that could result in schema changes and data loss. Are you sure you wish to continue?", true) {
				c.Printf("\n [CANCELLED] Migration cancelled!\n")
				return nil
			}
			steps, err := m.Migrate(ctx, target)
			printSteps(c, steps, false)
			if err != nil {
				return err
			}
			if len(steps) == 0 {
				c.Success("Already at the target version.")
				return nil
			}
			c.Success(fmt.Sprintf("Successfully executed %d migration(s).", len(steps)))
			return nil
		},
	}
}

func statusCmd() Command {
	return Command{
		Name:        "migrations:status",
		Description: "List migrations and whether they have been executed",
		Run: func(ctx context.Context, c *Ctx) error {
			m, err := c.App.Migrator()
			if err != nil {
				return err
			}
			if err := m.Init(ctx); err != nil {
				return err
			}
			st, err := m.Status(ctx)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, " Version\tStatus\tExecuted at\tDescription")
			pending := 0
			for _, s := range st {
				state, at := "not migrated", ""
				switch {
				case s.Unknown:
					state = "unknown (not registered)"
				case s.Applied:
					state = "migrated"
				default:
					pending++
				}
				if !s.ExecutedAt.IsZero() {
					at = s.ExecutedAt.Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(w, " %s\t%s\t%s\t%s\n", s.Version, state, at, s.Description)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			c.Printf("\n %d migration(s), %d pending\n", len(st), pending)
			return nil
		},
	}
}

func executeCmd() Command {
	var up, down *bool
	return Command{
		Name:        "migrations:execute",
		Usage:       "<version> --up|--down",
		Description: "Execute a single migration up or down",
		Flags: func(fs *flag.FlagSet) {
			up = fs.Bool("up", false, "execute the migration up")
			down = fs.Bool("down", false, "execute the migration down")
		},
		Run: func(ctx context.Context, c *Ctx) error {
			if c.Arg(0) == "" || *up == *down {
				return fmt.Errorf("usage: migrations:execute <version> --up|--down")
			}
			m, err := c.App.Migrator()
			if err != nil {
				return err
			}
			step, err := m.Execute(ctx, c.Arg(0), *up)
			if err != nil {
				return err
			}
			printSteps(c, []migrations.Step{step}, false)
			c.Success("Migration executed.")
			return nil
		},
	}
}

func formatFlag(fs *flag.FlagSet) *string {
	return fs.String("format", "", "migration file format: go or sql (default from config)")
}

func (a *App) format(f string) migrations.Format {
	if f != "" {
		return migrations.Format(f)
	}
	return a.Config.MigrationsFormat
}

func generateCmd() Command {
	var format, desc *string
	return Command{
		Name:        "migrations:generate",
		Description: "Generate a blank migration file",
		Flags: func(fs *flag.FlagSet) {
			format = formatFlag(fs)
			desc = fs.String("description", "", "migration description")
		},
		Run: func(_ context.Context, c *Ctx) error {
			files, err := migrations.Generate(migrations.GenerateOptions{Dir: c.App.Config.MigrationsDir, Format: c.App.format(*format), Description: *desc})
			if err != nil {
				return err
			}
			c.Success("Generated new migration: " + strings.Join(files, ", "))
			return nil
		},
	}
}

// DiffMigration generates a migration from the difference between entities
// and the database; it is shared with the maker's make:migration.
func DiffMigration(ctx context.Context, c *Ctx, format, desc string) error {
	a := c.App
	m, err := a.Migrator()
	if err != nil {
		return err
	}
	if err := m.Init(ctx); err != nil {
		return err
	}
	ents, err := a.Entities()
	if err != nil {
		return err
	}
	up, down, err := m.Diff(ctx, ents, a.ignore(m))
	if err != nil {
		return err
	}
	if len(up) == 0 {
		c.Printf("\n [OK] No changes detected in your mapping information.\n\n")
		return nil
	}
	files, err := migrations.Generate(migrations.GenerateOptions{Dir: a.Config.MigrationsDir, Format: a.format(format), Description: desc, Up: up, Down: down})
	if err != nil {
		return err
	}
	c.Success("Generated new migration: " + strings.Join(files, ", "))
	c.Printf(" Review it, then run migrations:migrate\n\n")
	return nil
}

func diffCmd() Command {
	var format, desc *string
	return Command{
		Name:        "migrations:diff",
		Description: "Generate a migration from the differences between your entities and the database",
		Flags: func(fs *flag.FlagSet) {
			format = formatFlag(fs)
			desc = fs.String("description", "", "migration description")
		},
		Run: func(ctx context.Context, c *Ctx) error { return DiffMigration(ctx, c, *format, *desc) },
	}
}

func validateCmd() Command {
	var skipSync *bool
	return Command{
		Name:        "schema:validate",
		Description: "Validate the mapping and check that the database is in sync",
		Flags:       func(fs *flag.FlagSet) { skipSync = fs.Bool("skip-sync", false, "do not compare with the database") },
		Run: func(ctx context.Context, c *Ctx) error {
			a := c.App
			ents, err := a.Entities()
			if err != nil {
				return err
			}
			opts := validate.Options{Entities: ents}
			if !*skipSync {
				m, err := a.Migrator()
				if err != nil {
					return err
				}
				opts.DB, _ = a.DB()
				opts.Ignore = a.ignore(m)
			}
			rep, err := validate.Run(ctx, opts)
			if err != nil {
				return err
			}
			c.Printf("\n%s\n", rep)
			if !rep.OK() {
				return fmt.Errorf("the schema is not valid")
			}
			return nil
		},
	}
}

func updateCmd() Command {
	var force, dump *bool
	return Command{
		Name:        "schema:update",
		Description: "Update the database schema directly from the mapping (development only; use migrations in production)",
		Flags: func(fs *flag.FlagSet) {
			force = fs.Bool("force", false, "execute the SQL")
			dump = fs.Bool("dump-sql", false, "print the SQL")
		},
		Run: func(ctx context.Context, c *Ctx) error {
			a := c.App
			db, err := a.DB()
			if err != nil {
				return err
			}
			m, _ := a.Migrator()
			ents, err := a.Entities()
			if err != nil {
				return err
			}
			rep, err := validate.Run(ctx, validate.Options{Entities: ents, DB: db, Ignore: a.ignore(m)})
			if err != nil {
				return err
			}
			if len(rep.Statements) == 0 {
				c.Success("Nothing to update - your database is already in sync with the current entity metadata.")
				return nil
			}
			if *dump || !*force {
				for _, q := range rep.Statements {
					c.Printf("%s;\n", q)
				}
			}
			if !*force {
				c.Printf("\n Run with --force to execute these %d statement(s).\n", len(rep.Statements))
				return nil
			}
			for _, q := range rep.Statements {
				if _, err := db.ExecContext(ctx, q); err != nil {
					return fmt.Errorf("%s: %w", q, err)
				}
			}
			c.Success(fmt.Sprintf("Database schema updated successfully (%d statements).", len(rep.Statements)))
			return nil
		},
	}
}
