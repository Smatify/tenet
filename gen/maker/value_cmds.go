package maker

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"

	"github.com/smatify/smorm/console"
	"github.com/smatify/smorm/gen/scaffold"
)

func makeValue(kind ValueKind) console.Command {
	return console.Command{
		Name:        "make:" + string(kind),
		Usage:       "[Name]",
		Description: "Create or update an " + string(kind) + " (reusable struct of mapped fields)",
		Run: func(_ context.Context, c *console.Ctx) error {
			dir := c.App.Config.EntityDir
			if dir == "" {
				return errors.New("no entity directory configured (Config.EntityDir)")
			}
			name := c.Arg(0)
			if name == "" {
				name = c.Ask("Name of the "+string(kind)+" (e.g. Address)", "")
			}
			if name == "" {
				return errors.New("a name is required")
			}
			name = upper(name)
			mk := &Maker{Dir: dir}
			files, err := mk.ApplyValue(kind, name, nil)
			if err != nil {
				return err
			}
			for _, f := range files {
				c.Printf(" written: %s\n", f)
			}
			for c.Interactive {
				prop := c.Ask("New property name (press <return> to stop adding fields)", "")
				if prop == "" {
					break
				}
				f := FieldSpec{Name: lower(prop)}
				scalars := FieldTypes[:len(FieldTypes)-2] // without relation and embedded
				f.Type = c.Choice("Field type", scalars, guessType(prop))
				if f.Type == "string" {
					f.Length = askInt(c, "Field length", 255)
				}
				f.Nullable = c.Confirm("Can this field be null in the database (nullable)", false)
				if _, err := mk.ApplyValue(kind, name, []FieldSpec{f}); err != nil {
					c.Printf("\n [ERROR] %v\n", err)
				}
			}
			c.Success("Success!")
			return nil
		},
	}
}

func makeFixtures() console.Command {
	var dir *string
	return console.Command{
		Name:        "make:fixtures",
		Usage:       "[Name]",
		Description: "Create a fixture for fixtures:load",
		Flags:       func(fs *flag.FlagSet) { dir = fs.String("dir", "fixtures", "fixtures package directory") },
		Run: func(_ context.Context, c *console.Ctx) error {
			name := c.Arg(0)
			if name == "" {
				name = c.Ask("The class name of the fixtures to create (e.g. AppFixtures)", "AppFixtures")
			}
			path, err := MakeFixtures(*dir, upper(name))
			if err != nil {
				return err
			}
			c.Printf(" created: %s\n", path)
			main := filepath.Join("cmd", "console", "main.go")
			if _, err := os.Stat(main); err == nil {
				if mod, err := scaffold.ModulePath("."); err == nil {
					added, err := EnsureBlankImport(main, mod+"/"+filepath.ToSlash(*dir))
					if err != nil {
						return err
					}
					if added {
						c.Printf(" updated: %s (imports the fixtures package)\n", main)
					}
				}
			}
			c.Success("Next: add your data in the Load function, then run fixtures:load")
			return nil
		},
	}
}
