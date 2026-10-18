// Package dbal is the database abstraction layer: connections, transactions,
// query hooks and the SQL builder.
package dbal

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"

	"github.com/smatify/smorm/dialect"
)

// ErrTxDone is returned when committing or rolling back a finished transaction.
var ErrTxDone = errors.New("dbal: transaction has already been committed or rolled back")

// IDB is implemented by *DB, *Conn and *Tx so code can run inside or outside
// a transaction transparently.
type IDB interface {
	Dialect() dialect.Dialect
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error)
	RunInTx(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context, tx *Tx) error) error
}

var (
	_ IDB = (*DB)(nil)
	_ IDB = (*Conn)(nil)
	_ IDB = (*Tx)(nil)
)

// DB is a database handle bound to a dialect. It is safe for concurrent use.
type DB struct {
	sql     *sql.DB
	dialect dialect.Dialect
	hooks   atomic.Pointer[[]QueryHook]
}

// Option configures a DB.
type Option func(*DB)

// WithQueryHook registers a query hook.
func WithQueryHook(h QueryHook) Option { return func(db *DB) { db.AddQueryHook(h) } }

// NewDB wraps an existing *sql.DB.
func NewDB(sqlDB *sql.DB, d dialect.Dialect, opts ...Option) *DB {
	db := &DB{sql: sqlDB, dialect: d}
	db.hooks.Store(&[]QueryHook{})
	for _, o := range opts {
		o(db)
	}
	return db
}

// Open opens a database with database/sql and wraps it.
func Open(driverName, dsn string, d dialect.Dialect, opts ...Option) (*DB, error) {
	sqlDB, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	return NewDB(sqlDB, d, opts...), nil
}

// AddQueryHook adds a hook. It is safe to call concurrently with queries.
func (db *DB) AddQueryHook(h QueryHook) {
	for {
		old := db.hooks.Load()
		next := append(append(make([]QueryHook, 0, len(*old)+1), *old...), h)
		if db.hooks.CompareAndSwap(old, &next) {
			return
		}
	}
}

// Dialect implements IDB.
func (db *DB) Dialect() dialect.Dialect { return db.dialect }

// SQL returns the underlying *sql.DB.
func (db *DB) SQL() *sql.DB { return db.sql }

// Close closes the database.
func (db *DB) Close() error { return db.sql.Close() }

// PingContext verifies the connection.
func (db *DB) PingContext(ctx context.Context) error { return db.sql.PingContext(ctx) }

// ExecContext implements IDB.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, ev := db.beforeQuery(ctx, db, query, args)
	res, err := db.sql.ExecContext(ctx, query, args...)
	db.afterQuery(ctx, ev, res, err)
	return res, err
}

// QueryContext implements IDB.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	ctx, ev := db.beforeQuery(ctx, db, query, args)
	rows, err := db.sql.QueryContext(ctx, query, args...)
	db.afterQuery(ctx, ev, nil, err)
	return rows, err
}

// QueryRowContext implements IDB.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	ctx, ev := db.beforeQuery(ctx, db, query, args)
	row := db.sql.QueryRowContext(ctx, query, args...)
	db.afterQuery(ctx, ev, nil, row.Err())
	return row
}

// Conn returns a single dedicated connection.
func (db *DB) Conn(ctx context.Context) (*Conn, error) {
	c, err := db.sql.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &Conn{db: db, conn: c}, nil
}

// BeginTx implements IDB.
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := db.sql.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return newRootTx(db, tx), nil
}

// RunInTx implements IDB.
func (db *DB) RunInTx(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context, tx *Tx) error) error {
	return runInTx(ctx, db, opts, fn)
}

// Conn is a single database connection.
type Conn struct {
	db   *DB
	conn *sql.Conn
}

// Dialect implements IDB.
func (c *Conn) Dialect() dialect.Dialect { return c.db.dialect }

// DB returns the parent DB.
func (c *Conn) DB() *DB { return c.db }

// Close returns the connection to the pool.
func (c *Conn) Close() error { return c.conn.Close() }

// ExecContext implements IDB.
func (c *Conn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, ev := c.db.beforeQuery(ctx, c, query, args)
	res, err := c.conn.ExecContext(ctx, query, args...)
	c.db.afterQuery(ctx, ev, res, err)
	return res, err
}

// QueryContext implements IDB.
func (c *Conn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	ctx, ev := c.db.beforeQuery(ctx, c, query, args)
	rows, err := c.conn.QueryContext(ctx, query, args...)
	c.db.afterQuery(ctx, ev, nil, err)
	return rows, err
}

// QueryRowContext implements IDB.
func (c *Conn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	ctx, ev := c.db.beforeQuery(ctx, c, query, args)
	row := c.conn.QueryRowContext(ctx, query, args...)
	c.db.afterQuery(ctx, ev, nil, row.Err())
	return row
}

// BeginTx implements IDB.
func (c *Conn) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := c.conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return newRootTx(c.db, tx), nil
}

// RunInTx implements IDB.
func (c *Conn) RunInTx(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context, tx *Tx) error) error {
	return runInTx(ctx, c, opts, fn)
}

func runInTx(ctx context.Context, idb IDB, opts *sql.TxOptions, fn func(ctx context.Context, tx *Tx) error) (err error) {
	tx, err := idb.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(ctx, tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		return err
	}
	return tx.Commit()
}
