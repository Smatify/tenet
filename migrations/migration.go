// Package migrations runs versioned schema migrations, like Doctrine
// Migrations. Migrations are Go files that register themselves in init or
// SQL files (<version>_<name>.up.sql / .down.sql) loaded from an fs.FS.
package migrations

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/brand"
)

// Migration is one versioned change.
type Migration struct {
	// Version orders migrations; generated versions are UTC timestamps
	// (YYYYMMDDHHMMSS), so lexical order is chronological.
	Version     string
	Description string
	Up          func(ctx context.Context, m *M) error
	Down        func(ctx context.Context, m *M) error
	// NoTransaction runs the migration outside a transaction (needed for
	// statements like CREATE INDEX CONCURRENTLY).
	NoTransaction bool
}

// M is passed to Up and Down. Queue SQL with AddSQL; it runs after the
// function returns. DB may be used for data migrations (nil in dry runs).
type M struct {
	db      dbal.IDB
	dialect dialect.Dialect
	stmts   []stmt
	skip    string
}

type stmt struct {
	sql  string
	args []any
}

// AddSQL queues a statement.
func (m *M) AddSQL(sql string, args ...any) { m.stmts = append(m.stmts, stmt{sql, args}) }

// DB returns the connection (inside the migration's transaction), or nil
// during a dry run.
func (m *M) DB() dbal.IDB { return m.db }

// Dialect returns the database dialect.
func (m *M) Dialect() dialect.Dialect { return m.dialect }

// Skip marks the migration as executed without running its statements.
func (m *M) Skip(reason string) { m.skip = reason }

// Statements returns the queued SQL (for dry runs).
func (m *M) Statements() []string {
	out := make([]string, len(m.stmts))
	for i, s := range m.stmts {
		out[i] = s.sql
	}
	return out
}

// Registry holds migrations by version.
type Registry struct {
	mu         sync.Mutex
	migrations map[string]*Migration
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{migrations: map[string]*Migration{}} }

// Default is the registry generated Go migrations register into.
var Default = NewRegistry()

// Register adds m to the default registry; it panics on invalid or
// duplicate migrations because it runs from init functions.
func Register(m Migration) {
	if err := Default.Add(m); err != nil {
		panic(err)
	}
}

var versionRe = regexp.MustCompile(`^[0-9A-Za-z_]+$`)

// Add registers m.
func (r *Registry) Add(m Migration) error {
	if !versionRe.MatchString(m.Version) {
		return fmt.Errorf("migrations: invalid version %q", m.Version)
	}
	if m.Up == nil {
		return fmt.Errorf("migrations: %s has no Up function", m.Version)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.migrations[m.Version]; dup {
		return fmt.Errorf("migrations: version %s is registered twice", m.Version)
	}
	r.migrations[m.Version] = &m
	return nil
}

// Get returns a migration by version.
func (r *Registry) Get(version string) (*Migration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.migrations[version]
	return m, ok
}

// All returns the migrations sorted by version.
func (r *Registry) All() []*Migration {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Migration, 0, len(r.migrations))
	for _, m := range r.migrations {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b *Migration) int { return strings.Compare(a.Version, b.Version) })
	return out
}

// SplitMarker separates statements in SQL migration files.
var SplitMarker = "--" + brand.Name + ":split"

// NoTxMarker anywhere in an up file runs the migration outside a transaction.
var NoTxMarker = "--" + brand.Name + ":no-transaction"

var sqlFileRe = regexp.MustCompile(`^([0-9A-Za-z_]+?)(?:_([A-Za-z0-9_-]*))?\.(up|down)\.sql$`)

// LoadSQL registers SQL migrations from dir in fsys (e.g. an embed.FS).
// Files are named <version>_<description>.up.sql and .down.sql; statements
// are separated by SplitMarker lines (otherwise the file runs as one batch).
func (r *Registry) LoadSQL(fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return err
	}
	type pair struct{ up, down, desc string }
	found := map[string]*pair{}
	for _, e := range entries {
		m := sqlFileRe.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		p := found[m[1]]
		if p == nil {
			p = &pair{desc: strings.ReplaceAll(m[2], "_", " ")}
			found[m[1]] = p
		}
		if m[3] == "up" {
			p.up = string(b)
		} else {
			p.down = string(b)
		}
	}
	for version, p := range found {
		if p.up == "" {
			return fmt.Errorf("migrations: %s has a down file but no up file", version)
		}
		mig := Migration{Version: version, Description: p.desc, Up: sqlFunc(p.up), NoTransaction: strings.Contains(p.up, NoTxMarker)}
		if p.down != "" {
			mig.Down = sqlFunc(p.down)
		}
		if err := r.Add(mig); err != nil {
			return err
		}
	}
	return nil
}

// SplitSQL splits a script at SplitMarker lines and drops empty parts.
func SplitSQL(script string) []string {
	var out []string
	var cur []string
	flush := func() {
		if s := strings.TrimSpace(strings.Join(cur, "\n")); s != "" && !onlyComments(s) {
			out = append(out, s)
		}
		cur = nil
	}
	for _, line := range strings.Split(script, "\n") {
		if strings.TrimSpace(line) == SplitMarker {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

func onlyComments(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "--") {
			return false
		}
	}
	return true
}

func sqlFunc(script string) func(context.Context, *M) error {
	return func(_ context.Context, m *M) error {
		for _, s := range SplitSQL(script) {
			m.AddSQL(s)
		}
		return nil
	}
}

// ErrIrreversible is returned when migrating down a migration without Down.
var ErrIrreversible = errors.New("migrations: migration has no Down function")
