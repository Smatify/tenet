// Package maker adds the make:* commands (the MakerBundle equivalent) to a
// project console.
package maker

import (
	"context"
	"flag"
	"fmt"

	"github.com/smatify/smorm/console"
	"github.com/smatify/smorm/gen"
)

// Register adds the maker commands to app.
func Register(app *console.App) {
	app.Add(makeMigration(), generateCmd())
	app.Add(makeEntity(), makeValue(KindEmbeddable), makeValue(KindTrait), makeFixtures())
}

func makeMigration() console.Command {
	var format, desc *string
	return console.Command{
		Name:        "make:migration",
		Description: "Create a migration from the differences between entities and the database",
		Flags: func(fs *flag.FlagSet) {
			format = fs.String("format", "", "go or sql (default from config)")
			desc = fs.String("description", "", "migration description")
		},
		Run: func(ctx context.Context, c *console.Ctx) error { return console.DiffMigration(ctx, c, *format, *desc) },
	}
}

func generateCmd() console.Command {
	return console.Command{
		Name:        "make:generate",
		Usage:       "[dir...]",
		Description: "Regenerate entity accessors and field references",
		Run: func(_ context.Context, c *console.Ctx) error {
			dirs := c.Args
			if len(dirs) == 0 {
				dirs = []string{c.App.Config.EntityDir}
			}
			for _, d := range dirs {
				if d == "" {
					return fmt.Errorf("no entity directory configured")
				}
				out, err := gen.Generate(d)
				if err != nil {
					return err
				}
				if out != "" {
					c.Printf(" updated: %s\n", out)
				}
			}
			c.Success("Generated code is up to date.")
			return nil
		},
	}
}
