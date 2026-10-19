// Package fixtures loads test and demo data, like DoctrineFixturesBundle.
// Fixtures register themselves (usually from init in a fixtures package),
// may depend on each other, share entities through references and belong to
// groups.
package fixtures

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/orm"
	"github.com/smatify/smorm/schema"
)

// Fixture creates data.
type Fixture struct {
	Name      string
	DependsOn []string // fixtures that must run first
	Groups    []string
	Load      func(ctx context.Context, f *Context) error
}

// Context is passed to fixtures.
type Context struct {
	EM   *orm.EntityManager
	refs map[string]any
}

// AddReference stores an entity under name for later fixtures.
func (c *Context) AddReference(name string, entity any) { c.refs[name] = entity }

// Reference returns a stored entity (nil if missing).
func (c *Context) Reference(name string) any { return c.refs[name] }

// HasReference reports whether name was stored.
func (c *Context) HasReference(name string) bool { _, ok := c.refs[name]; return ok }

// Ref returns the reference name as *T; it panics with a clear message if
// the reference is missing or of another type.
func Ref[T any](c *Context, name string) *T {
	v, ok := c.refs[name].(*T)
	if !ok {
		panic(fmt.Sprintf("fixtures: reference %q is missing or not a %T", name, v))
	}
	return v
}

// Registry holds fixtures.
type Registry struct {
	mu       sync.Mutex
	fixtures map[string]Fixture
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{fixtures: map[string]Fixture{}} }

// Default is the registry fixtures register into.
var Default = NewRegistry()

// Register adds f to the default registry (panics on duplicates).
func Register(f Fixture) {
	if err := Default.Add(f); err != nil {
		panic(err)
	}
}

// Add registers f.
func (r *Registry) Add(f Fixture) error {
	if f.Name == "" || f.Load == nil {
		return errors.New("fixtures: a fixture needs a name and a Load function")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.fixtures[f.Name]; dup {
		return fmt.Errorf("fixtures: %q is registered twice", f.Name)
	}
	r.fixtures[f.Name] = f
	return nil
}

// Ordered returns the fixtures of the groups (all if none) in dependency order.
// Dependencies of selected fixtures are included even outside the groups.
func (r *Registry) Ordered(groups ...string) ([]Fixture, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.fixtures))
	for n, f := range r.fixtures {
		if len(groups) == 0 || slices.ContainsFunc(f.Groups, func(g string) bool { return slices.Contains(groups, g) }) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	state := map[string]int{}
	var out []Fixture
	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		f, ok := r.fixtures[name]
		if !ok {
			return fmt.Errorf("fixtures: %s depends on unknown fixture %q", path[len(path)-1], name)
		}
		switch state[name] {
		case 1:
			return fmt.Errorf("fixtures: dependency cycle %s", strings.Join(append(path, name), " -> "))
		case 2:
			return nil
		}
		state[name] = 1
		deps := slices.Clone(f.DependsOn)
		sort.Strings(deps)
		for _, d := range deps {
			if err := visit(d, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = 2
		out = append(out, f)
		return nil
	}
	for _, n := range names {
		if err := visit(n, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Options configures Load.
type Options struct {
	Groups []string
	// Append keeps existing rows; otherwise the tables of Entities are purged.
	Append   bool
	Entities []*mapping.Entity
	// Exclude keeps tables (e.g. the migrations table) out of the purge.
	Exclude func(table string) bool
}

// Load purges the mapped tables (unless Append) and runs the fixtures in one
// transaction. It returns the names of the executed fixtures.
func (r *Registry) Load(ctx context.Context, db *dbal.DB, reg *mapping.Registry, opts Options) ([]string, error) {
	list, err := r.Ordered(opts.Groups...)
	if err != nil {
		return nil, err
	}
	var done []string
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx *dbal.Tx) error {
		if !opts.Append {
			if err := Purge(ctx, tx, opts.Entities, opts.Exclude); err != nil {
				return err
			}
		}
		fc := &Context{EM: orm.New(tx, orm.WithRegistry(reg), orm.WithContext(ctx)), refs: map[string]any{}}
		for _, f := range list {
			if err := f.Load(ctx, fc); err != nil {
				return fmt.Errorf("fixtures: %s: %w", f.Name, err)
			}
			if err := fc.EM.Flush(ctx); err != nil {
				return fmt.Errorf("fixtures: %s: %w", f.Name, err)
			}
			done = append(done, f.Name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return done, nil
}

// Purge deletes all rows of the entities' tables (and join tables),
// referencing tables first.
func Purge(ctx context.Context, db dbal.IDB, entities []*mapping.Entity, exclude func(string) bool) error {
	tables := schema.DependencyOrder(mapping.BuildSchema(entities).Tables)
	mysql := db.Dialect().Name() == dialect.MySQL || db.Dialect().Name() == dialect.MariaDB
	if mysql { // self references are checked row by row in MySQL
		if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
			return err
		}
		defer func() { _, _ = db.ExecContext(context.WithoutCancel(ctx), "SET FOREIGN_KEY_CHECKS = 1") }()
	}
	for _, t := range tables {
		if exclude != nil && exclude(t.Name) {
			continue
		}
		if _, err := db.NewDelete().Table(t.Name).AllRows().Exec(ctx); err != nil {
			return fmt.Errorf("fixtures: purging %s: %w", t.Name, err)
		}
	}
	return nil
}
