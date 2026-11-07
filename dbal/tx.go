package dbal

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/smatify/tenet/dialect"
)

// Tx is a database transaction. Calling BeginTx or RunInTx on a Tx starts a
// nested transaction implemented with savepoints.
type Tx struct {
	db        *DB
	tx        *sql.Tx
	root      *txRoot
	savepoint string // empty for the outermost transaction
	ctx       context.Context
	done      bool
}

type txRoot struct {
	mu      sync.Mutex
	counter int
}

func newRootTx(db *DB, tx *sql.Tx) *Tx {
	return &Tx{db: db, tx: tx, root: &txRoot{}}
}

// Dialect implements IDB.
func (tx *Tx) Dialect() dialect.Dialect { return tx.db.dialect }

// DB returns the parent DB.
func (tx *Tx) DB() *DB { return tx.db }

// SQL returns the underlying *sql.Tx.
func (tx *Tx) SQL() *sql.Tx { return tx.tx }

// IsNested reports whether tx is a savepoint inside another transaction.
func (tx *Tx) IsNested() bool { return tx.savepoint != "" }

// ExecContext implements IDB.
func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, ev := tx.db.beforeQuery(ctx, tx, query, args)
	res, err := tx.tx.ExecContext(ctx, query, args...)
	tx.db.afterQuery(ctx, ev, res, err)
	return res, err
}

// QueryContext implements IDB.
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	ctx, ev := tx.db.beforeQuery(ctx, tx, query, args)
	rows, err := tx.tx.QueryContext(ctx, query, args...)
	tx.db.afterQuery(ctx, ev, nil, err)
	return rows, err
}

// QueryRowContext implements IDB.
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	ctx, ev := tx.db.beforeQuery(ctx, tx, query, args)
	row := tx.tx.QueryRowContext(ctx, query, args...)
	tx.db.afterQuery(ctx, ev, nil, row.Err())
	return row
}

// BeginTx starts a nested transaction using a savepoint. opts are ignored.
func (tx *Tx) BeginTx(ctx context.Context, _ *sql.TxOptions) (*Tx, error) {
	if tx.done {
		return nil, ErrTxDone
	}
	if !tx.db.dialect.Features().Has(dialect.FeatureSavepoint) {
		return nil, fmt.Errorf("dbal: dialect %s does not support savepoints", tx.db.dialect.Name())
	}
	tx.root.mu.Lock()
	tx.root.counter++
	name := fmt.Sprintf("sp_%d", tx.root.counter)
	tx.root.mu.Unlock()
	if _, err := tx.ExecContext(ctx, tx.db.dialect.SavepointSQL(name)); err != nil {
		return nil, err
	}
	return &Tx{db: tx.db, tx: tx.tx, root: tx.root, savepoint: name, ctx: ctx}, nil
}

// RunInTx runs fn in a nested transaction (savepoint).
func (tx *Tx) RunInTx(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context, tx *Tx) error) error {
	return runInTx(ctx, tx, opts, fn)
}

// Commit commits the transaction or releases the savepoint.
func (tx *Tx) Commit() error {
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	if tx.savepoint == "" {
		return tx.tx.Commit()
	}
	q := tx.db.dialect.ReleaseSavepointSQL(tx.savepoint)
	if q == "" {
		return nil
	}
	_, err := tx.ExecContext(tx.context(), q)
	return err
}

// Rollback aborts the transaction or rolls back to the savepoint.
func (tx *Tx) Rollback() error {
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	if tx.savepoint == "" {
		return tx.tx.Rollback()
	}
	_, err := tx.ExecContext(tx.context(), tx.db.dialect.RollbackToSavepointSQL(tx.savepoint))
	return err
}

func (tx *Tx) context() context.Context {
	if tx.ctx != nil {
		return context.WithoutCancel(tx.ctx)
	}
	return context.Background()
}
