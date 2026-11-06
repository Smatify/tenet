package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/internal/brand"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/types"
)

// ErrLocked is returned when another process holds the migration lock.
var ErrLocked = errors.New("migrations: another migration run holds the lock")

// Migrator applies migrations of a registry to a database.
type Migrator struct {
	db        *dbal.DB
	reg       *Registry
	table     string
	lockTable string
	now       func() time.Time
	group     int // group of the current run
}

// Option configures a Migrator.
type Option func(*Migrator)

// WithTable sets the versions table name.
func WithTable(name string) Option { return func(m *Migrator) { m.table = name } }

// WithLockTable sets the lock table name.
func WithLockTable(name string) Option { return func(m *Migrator) { m.lockTable = name } }

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(m *Migrator) { m.now = now } }

// New returns a migrator for db using reg (Default if nil).
func New(db *dbal.DB, reg *Registry, opts ...Option) *Migrator {
	if reg == nil {
		reg = Default
	}
	m := &Migrator{db: db, reg: reg, table: brand.MigrationsTable, lockTable: brand.MigrationLocksTable, now: time.Now}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Tables returns the names of the bookkeeping tables (to exclude them from
// schema comparisons).
func (m *Migrator) Tables() []string { return []string{m.table, m.lockTable} }

// IsOwnTable reports whether name is one of the bookkeeping tables.
func (m *Migrator) IsOwnTable(name string) bool {
	return strings.EqualFold(name, m.table) || strings.EqualFold(name, m.lockTable)
}

// metadataSchema describes the bookkeeping tables.
func (m *Migrator) metadataSchema() *schema.Schema {
	s := schema.New()
	v := s.CreateTable(m.table)
	v.AddColumn("version", types.String).SetLength(191)
	v.AddColumn("executed_at", types.DateTime).SetNullable(true)
	v.AddColumn("execution_time", types.Integer).SetNullable(true)
	v.AddColumn("group_id", types.Integer).SetNullable(true)
	v.SetPrimaryKey("version")
	l := s.CreateTable(m.lockTable)
	l.AddColumn("id", types.Integer)
	l.AddColumn("locked_at", types.DateTime)
	l.SetPrimaryKey("id")
	return s
}

func (m *Migrator) tableExists(ctx context.Context, name string) bool {
	var n int
	return m.db.NewSelect().ColumnExpr("COUNT(*)").Table(name).Scan(ctx, &n) == nil
}

// Init creates the bookkeeping tables if they do not exist.
func (m *Migrator) Init(ctx context.Context) error {
	ddl := schema.NewDDL(m.db.Dialect())
	for _, t := range m.metadataSchema().Tables {
		if m.tableExists(ctx, t.Name) {
			if t.Name == m.table {
				if err := m.upgradeTable(ctx, ddl, t); err != nil {
					return err
				}
			}
			continue
		}
		stmts, err := ddl.CreateTable(t)
		if err != nil {
			return err
		}
		for _, s := range stmts {
			if _, err := m.db.ExecContext(ctx, s); err != nil {
				return fmt.Errorf("migrations: creating %s: %w", t.Name, err)
			}
		}
	}
	return nil
}

// upgradeTable adds the group_id column to versions tables created by
// older versions.
func (m *Migrator) upgradeTable(ctx context.Context, ddl *schema.DDL, t *schema.Table) error {
	// Unquoted on purpose: SQLite treats an unknown double-quoted identifier
	// as a string literal, which would hide the missing column.
	var n int
	if m.db.NewSelect().ColumnExpr("COUNT(group_id)").Table(m.table).Scan(ctx, &n) == nil {
		return nil
	}
	diff := &schema.Diff{AlterTables: []*schema.TableDiff{{From: t, To: t, AddColumns: []*schema.Column{t.Column("group_id")}}}}
	stmts, err := ddl.Migrate(diff)
	if err != nil {
		return err
	}
	for _, s := range stmts {
		if _, err := m.db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrations: upgrading %s: %w", m.table, err)
		}
	}
	return nil
}

// Lock acquires the migration lock. Release it with Unlock.
func (m *Migrator) Lock(ctx context.Context) error {
	now, err := types.Default.MustLookup(types.DateTime).ToDatabase(m.now(), m.db.Dialect().Name())
	if err != nil {
		return err
	}
	if _, err := m.db.NewInsert().Table(m.lockTable).Columns("id", "locked_at").Values(1, now).Exec(ctx); err != nil {
		return fmt.Errorf("%w (%v); remove the row from %s if the previous run crashed", ErrLocked, err, m.lockTable)
	}
	return nil
}

// Unlock releases the migration lock.
func (m *Migrator) Unlock(ctx context.Context) error {
	_, err := m.db.NewDelete().Table(m.lockTable).Where("id = ?", 1).Exec(ctx)
	return err
}

// Applied returns the executed versions with their execution time.
func (m *Migrator) Applied(ctx context.Context) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if !m.tableExists(ctx, m.table) { // a new database: nothing applied yet (Init creates the table)
		return out, nil
	}
	d := m.db.Dialect().Name()
	dt := types.Default.MustLookup(types.DateTime)
	rows, err := m.db.NewSelect().Column("version", "executed_at").Table(m.table).Rows(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var at *time.Time
		if err := rows.Scan(&v, dt.ScanTarget(&at, d)); err != nil {
			return nil, err
		}
		if at != nil {
			out[v] = *at
		} else {
			out[v] = time.Time{}
		}
	}
	return out, rows.Err()
}

// Status describes one migration.
type Status struct {
	Version     string
	Description string
	Applied     bool
	ExecutedAt  time.Time
	// Unknown is true for versions recorded in the database that are not
	// registered (e.g. deleted migration files).
	Unknown bool
}

// Status lists registered and unknown applied migrations by version.
func (m *Migrator) Status(ctx context.Context) ([]Status, error) {
	applied, err := m.Applied(ctx)
	if err != nil {
		return nil, err
	}
	var out []Status
	for _, mig := range m.reg.All() {
		at, ok := applied[mig.Version]
		out = append(out, Status{Version: mig.Version, Description: mig.Description, Applied: ok, ExecutedAt: at})
		delete(applied, mig.Version)
	}
	for v, at := range applied {
		out = append(out, Status{Version: v, Applied: true, ExecutedAt: at, Unknown: true})
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.Version, b.Version) })
	return out, nil
}

// Step is one planned or executed migration.
type Step struct {
	Migration *Migration
	Up        bool
	SQL       []string
	Duration  time.Duration
	Skipped   string
}

// Plan resolves target into the steps needed to reach it. target is a
// version, "latest" (or ""), "first" (revert everything), "prev" or "next".
func (m *Migrator) Plan(ctx context.Context, target string) ([]Step, error) {
	applied, err := m.Applied(ctx)
	if err != nil {
		return nil, err
	}
	all := m.reg.All()
	current := ""
	for v := range applied {
		current = max(current, v)
	}
	switch target {
	case "", "latest":
		if len(all) == 0 {
			return nil, nil
		}
		target = all[len(all)-1].Version
	case "first":
		target = ""
	case "prev":
		if current == "" {
			return nil, errors.New("migrations: nothing to roll back")
		}
		target = ""
		for v := range applied {
			if v < current {
				target = max(target, v)
			}
		}
	case "next":
		target = ""
		for _, mig := range all {
			if _, ok := applied[mig.Version]; !ok {
				target = mig.Version
				break
			}
		}
		if target == "" {
			return nil, nil
		}
	default:
		if _, ok := m.reg.Get(target); !ok {
			return nil, fmt.Errorf("migrations: unknown version %q", target)
		}
	}
	var steps []Step
	var down []string
	for v := range applied {
		if v > target {
			down = append(down, v)
		}
	}
	slices.Sort(down)
	slices.Reverse(down)
	for _, v := range down {
		mig, ok := m.reg.Get(v)
		if !ok {
			return nil, fmt.Errorf("migrations: cannot revert %s: it is not registered", v)
		}
		steps = append(steps, Step{Migration: mig, Up: false})
	}
	for _, mig := range all {
		if _, ok := applied[mig.Version]; !ok && mig.Version <= target {
			steps = append(steps, Step{Migration: mig, Up: true})
		}
	}
	return steps, nil
}

// Migrate brings the database to target (see Plan). Each migration runs in
// its own transaction unless it sets NoTransaction. It returns the executed
// steps; on error the steps before the failing one stay applied.
func (m *Migrator) Migrate(ctx context.Context, target string) ([]Step, error) {
	return m.run(ctx, target, false)
}

// DryRun returns the SQL Migrate would execute without changing anything.
// Data migrations that use M.DB() see a nil DB.
func (m *Migrator) DryRun(ctx context.Context, target string) ([]Step, error) {
	return m.run(ctx, target, true)
}

func (m *Migrator) run(ctx context.Context, target string, dry bool) ([]Step, error) {
	if err := m.Init(ctx); err != nil {
		return nil, err
	}
	if !dry {
		if err := m.Lock(ctx); err != nil {
			return nil, err
		}
		defer func() { _ = m.Unlock(context.WithoutCancel(ctx)) }()
	}
	steps, err := m.Plan(ctx, target)
	if err != nil {
		return nil, err
	}
	if m.group, err = m.nextGroup(ctx); err != nil {
		return nil, err
	}
	for i := range steps {
		if err := m.execute(ctx, &steps[i], dry); err != nil {
			return steps[:i], err
		}
	}
	return steps, nil
}

// Execute runs a single migration up or down regardless of order, like
// Doctrine's migrations:execute.
func (m *Migrator) Execute(ctx context.Context, version string, up bool) (Step, error) {
	mig, ok := m.reg.Get(version)
	if !ok {
		return Step{}, fmt.Errorf("migrations: unknown version %q", version)
	}
	if err := m.Init(ctx); err != nil {
		return Step{}, err
	}
	applied, err := m.Applied(ctx)
	if err != nil {
		return Step{}, err
	}
	if _, isApplied := applied[version]; isApplied == up {
		state := "already applied"
		if !up {
			state = "not applied"
		}
		return Step{}, fmt.Errorf("migrations: %s is %s", version, state)
	}
	if err := m.Lock(ctx); err != nil {
		return Step{}, err
	}
	defer func() { _ = m.Unlock(context.WithoutCancel(ctx)) }()
	if m.group, err = m.nextGroup(ctx); err != nil {
		return Step{}, err
	}
	step := Step{Migration: mig, Up: up}
	return step, m.execute(ctx, &step, false)
}

func (m *Migrator) execute(ctx context.Context, step *Step, dry bool) error {
	mig := step.Migration
	fn := mig.Up
	if !step.Up {
		if mig.Down == nil {
			return fmt.Errorf("%w: %s", ErrIrreversible, mig.Version)
		}
		fn = mig.Down
	}
	start := m.now()
	body := func(ctx context.Context, db dbal.IDB) error {
		mm := &M{db: db, dialect: m.db.Dialect()}
		if err := fn(ctx, mm); err != nil {
			return err
		}
		step.SQL = mm.Statements()
		step.Skipped = mm.skip
		if dry {
			return nil
		}
		if mm.skip == "" {
			for _, s := range mm.stmts {
				if _, err := db.ExecContext(ctx, s.sql, s.args...); err != nil {
					return fmt.Errorf("%s\n-> %w", s.sql, err)
				}
			}
		}
		return m.record(ctx, db, mig.Version, step.Up, m.now().Sub(start))
	}
	var err error
	switch {
	case dry:
		err = body(ctx, nil)
	case mig.NoTransaction:
		err = body(ctx, m.db)
	default:
		err = m.db.RunInTx(ctx, nil, func(ctx context.Context, tx *dbal.Tx) error { return body(ctx, tx) })
	}
	step.Duration = m.now().Sub(start)
	if err != nil {
		dir := "up"
		if !step.Up {
			dir = "down"
		}
		return fmt.Errorf("migrations: %s (%s) failed: %w", mig.Version, dir, err)
	}
	return nil
}

func (m *Migrator) record(ctx context.Context, db dbal.IDB, version string, up bool, d time.Duration) error {
	if !up {
		_, err := db.NewDelete().Table(m.table).Where("version = ?", version).Exec(ctx)
		return err
	}
	at, err := types.Default.MustLookup(types.DateTime).ToDatabase(m.now(), m.db.Dialect().Name())
	if err != nil {
		return err
	}
	_, err = db.NewInsert().Table(m.table).Columns("version", "executed_at", "execution_time", "group_id").
		Values(version, at, int(d.Milliseconds()), m.group).Exec(ctx)
	return err
}

func (m *Migrator) maxGroup(ctx context.Context) (*int, error) {
	var g *int
	err := m.db.NewSelect().ColumnExpr("MAX(?)", dbal.Ident("group_id")).Table(m.table).Scan(ctx, &g)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return g, err
}

// nextGroup returns the group number for a new run.
func (m *Migrator) nextGroup(ctx context.Context) (int, error) {
	g, err := m.maxGroup(ctx)
	if err != nil {
		return 0, err
	}
	if g == nil {
		return 1, nil
	}
	return *g + 1, nil
}

// LastGroup returns the versions applied by the most recent run, newest first.
func (m *Migrator) LastGroup(ctx context.Context) ([]string, error) {
	g, err := m.maxGroup(ctx)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, nil
	}
	var out []string
	err = m.db.NewSelect().Column("version").Table(m.table).Where("? = ?", dbal.Ident("group_id"), *g).
		OrderBy("version", dbal.Desc).ForEach(ctx, func(r *sql.Rows) error {
		var v string
		if err := r.Scan(&v); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	return out, err
}

// Rollback reverts every migration applied by the most recent run (or
// Execute call), newest first. With dry it only returns the SQL.
func (m *Migrator) Rollback(ctx context.Context, dry bool) ([]Step, error) {
	if err := m.Init(ctx); err != nil {
		return nil, err
	}
	if !dry {
		if err := m.Lock(ctx); err != nil {
			return nil, err
		}
		defer func() { _ = m.Unlock(context.WithoutCancel(ctx)) }()
	}
	versions, err := m.LastGroup(ctx)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, errors.New("migrations: nothing to roll back")
	}
	var steps []Step
	for _, v := range versions {
		mig, ok := m.reg.Get(v)
		if !ok {
			return nil, fmt.Errorf("migrations: cannot revert %s: it is not registered", v)
		}
		steps = append(steps, Step{Migration: mig})
	}
	for i := range steps {
		if err := m.execute(ctx, &steps[i], dry); err != nil {
			return steps[:i], err
		}
	}
	return steps, nil
}
