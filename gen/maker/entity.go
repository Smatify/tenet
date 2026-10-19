package maker

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/smatify/smorm/console"
	"github.com/smatify/smorm/gen"
)

func makeEntity() console.Command {
	var style *string
	var uuidID, timestamps *bool
	return console.Command{
		Name:        "make:entity",
		Usage:       "[EntityName]",
		Description: "Create or update an entity, its fields, relations and repository",
		Flags: func(fs *flag.FlagSet) {
			style = fs.String("style", string(StyleDoctrine), "doctrine (getters/setters, lazy loading) or go (exported fields)")
			uuidID = fs.Bool("uuid", false, "use a UUIDv7 identifier for new entities")
			timestamps = fs.Bool("timestamps", false, "add created_at/updated_at to new entities")
		},
		Run: func(_ context.Context, c *console.Ctx) error {
			dir := c.App.Config.EntityDir
			if dir == "" {
				return errors.New("no entity directory configured (Config.EntityDir)")
			}
			st := Style(*style)
			if st != StyleDoctrine && st != StyleGo {
				return fmt.Errorf("unknown style %q", *style)
			}
			mk := &Maker{Dir: dir, Style: st}
			name := c.Arg(0)
			if name == "" {
				name = c.Ask("Class name of the entity to create or update (e.g. BravePuppy)", "")
			}
			if name == "" {
				return errors.New("an entity name is required")
			}
			name = upper(name)
			spec := EntitySpec{Name: name, UUID: *uuidID, Timestamps: *timestamps}
			path := (&writer{mk: mk}).path(name)
			if _, err := os.Stat(path); err == nil {
				c.Printf("\n Your entity already exists! So let's add some new fields!\n")
			} else {
				if !*timestamps && c.Interactive {
					spec.Timestamps = c.Confirm("Add created_at/updated_at timestamps?", false)
				}
				files, err := mk.Apply(spec)
				if err != nil {
					return err
				}
				for _, f := range files {
					c.Printf(" created: %s\n", f)
				}
			}
			for c.Interactive {
				f, ok := askField(c, name)
				if !ok {
					break
				}
				files, err := mk.Apply(EntitySpec{Name: name, Fields: []FieldSpec{f}})
				if err != nil {
					c.Printf("\n [ERROR] %v\n", err)
					continue
				}
				for _, p := range files {
					c.Printf(" updated: %s\n", p)
				}
			}
			if _, err := gen.Generate(dir); err != nil {
				return fmt.Errorf("generating accessors: %w", err)
			}
			c.Success("Success!")
			c.Printf(" Next: when you're ready, create a migration with make:migration\n\n")
			return nil
		},
	}
}

// guessType suggests a field type from the property name like MakerBundle.
func guessType(prop string) string {
	l := strings.ToLower(prop)
	switch {
	case strings.HasSuffix(prop, "At") || strings.HasSuffix(l, "_at"):
		return "datetime"
	case strings.HasPrefix(l, "is") || strings.HasPrefix(l, "has"):
		return "boolean"
	case strings.HasSuffix(l, "date"):
		return "date"
	case l == "uuid":
		return "uuid"
	}
	return "string"
}

func askInt(c *console.Ctx, q string, def int) int {
	for {
		v := c.Ask(q, strconv.Itoa(def))
		n, err := strconv.Atoi(v)
		if err == nil && n >= 0 {
			return n
		}
		c.Printf(" [ERROR] %q is not a number\n", v)
	}
}

// askField runs the property wizard; ok is false when the user is done.
func askField(c *console.Ctx, entity string) (FieldSpec, bool) {
	prop := c.Ask("New property name (press <return> to stop adding fields)", "")
	if prop == "" {
		return FieldSpec{}, false
	}
	f := FieldSpec{Name: lower(prop)}
	f.Type = c.Choice("Field type", FieldTypes, guessType(prop))
	switch f.Type {
	case "relation":
		askRelation(c, entity, &f)
		return f, true
	case "string":
		f.Length = askInt(c, "Field length", 255)
	case "decimal":
		f.Precision = askInt(c, "Precision (number of digits)", 10)
		f.Scale = askInt(c, "Scale (number of decimals)", 2)
	}
	f.Nullable = c.Confirm("Can this field be null in the database (nullable)", false)
	f.Unique = c.Confirm("Should the values be unique", false)
	return f, true
}

func askRelation(c *console.Ctx, entity string, f *FieldSpec) {
	f.Target = upper(c.Ask("What class should this entity be related to?", ""))
	kinds := []string{ManyToOne, OneToMany, ManyToMany, OneToOne}
	c.Printf("\n  %-11s Each %s relates to (has) one %s; each %s can have many %s\n", ManyToOne, entity, f.Target, f.Target, entity)
	c.Printf("  %-11s Each %s can relate to (have) many %s; each %s relates to one %s\n", OneToMany, entity, f.Target, f.Target, entity)
	c.Printf("  %-11s Each %s can have many %s; each %s can also have many %s\n", ManyToMany, entity, f.Target, f.Target, entity)
	c.Printf("  %-11s Each %s relates to exactly one %s and vice versa\n", OneToOne, entity, f.Target)
	f.Relation = c.Choice("Relation type", kinds, ManyToOne)
	switch f.Relation {
	case ManyToOne, OneToOne:
		f.Nullable = c.Confirm(fmt.Sprintf("Is the %s.%s property allowed to be null (nullable)?", entity, f.Name), true)
		if c.Confirm(fmt.Sprintf("Do you want to add a new property to %s so that you can access/update %s objects from it?", f.Target, entity), true) {
			def := lower(entity)
			if f.Relation == ManyToOne {
				def = plural(lower(entity))
			}
			f.Inverse = c.Ask(fmt.Sprintf("New field name inside %s", f.Target), def)
			if f.Relation == ManyToOne && !f.Nullable {
				f.OrphanRemoval = c.Confirm(fmt.Sprintf("Do you want to activate orphanRemoval on %s.%s?", f.Target, f.Inverse), false)
			}
		}
	case OneToMany:
		f.Inverse = c.Ask(fmt.Sprintf("A new property will also be added to %s so that you can access the related %s object from it. New field name inside %s", f.Target, entity, f.Target), lower(entity))
		f.Nullable = c.Confirm(fmt.Sprintf("Is the %s.%s property allowed to be null (nullable)?", f.Target, f.Inverse), true)
		if !f.Nullable {
			f.OrphanRemoval = c.Confirm(fmt.Sprintf("Do you want to activate orphanRemoval on %s.%s?", entity, f.Name), false)
		}
	case ManyToMany:
		if c.Confirm(fmt.Sprintf("Do you want to add a new property to %s so that you can access/update %s objects from it?", f.Target, entity), true) {
			f.Inverse = c.Ask(fmt.Sprintf("New field name inside %s", f.Target), plural(lower(entity)))
		}
	}
}
