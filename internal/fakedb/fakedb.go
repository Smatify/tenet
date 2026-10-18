// Package fakedb is an in-memory database/sql driver for unit tests. It records
// every statement and can be programmed with results and errors.
package fakedb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
)

// Recorder is a driver.Connector that records executed statements.
type Recorder struct {
	mu      sync.Mutex
	log     []Call
	fails   []rule
	results []rule
}

// Call is a recorded statement.
type Call struct {
	Query string
	Args  []any
}

type rule struct {
	substr  string
	err     error
	columns []string
	rows    [][]driver.Value
	once    bool
	used    bool
}

// New returns a new Recorder.
func New() *Recorder { return &Recorder{} }

// DB opens a *sql.DB backed by r.
func (r *Recorder) DB() *sql.DB { return sql.OpenDB(r) }

// FailOn makes every statement containing substr fail with err.
func (r *Recorder) FailOn(substr string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fails = append(r.fails, rule{substr: substr, err: err})
}

// Returns makes queries containing substr return the given rows (first match wins).
func (r *Recorder) Returns(substr string, columns []string, rows ...[]driver.Value) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, rule{substr: substr, columns: columns, rows: rows})
}

// ReturnsOnce is like Returns but the rule is only used once.
func (r *Recorder) ReturnsOnce(substr string, columns []string, rows ...[]driver.Value) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, rule{substr: substr, columns: columns, rows: rows, once: true})
}

// Calls returns all recorded calls.
func (r *Recorder) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.log...)
}

// Queries returns all recorded statement texts.
func (r *Recorder) Queries() []string {
	calls := r.Calls()
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Query
	}
	return out
}

// Reset clears the log.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = nil
}

func (r *Recorder) record(q string, args []driver.NamedValue) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	vals := make([]any, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	r.log = append(r.log, Call{Query: q, Args: vals})
	for _, f := range r.fails {
		if strings.Contains(q, f.substr) {
			return f.err
		}
	}
	return nil
}

func (r *Recorder) rowsFor(q string) *rows {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.results {
		res := &r.results[i]
		if res.once && res.used {
			continue
		}
		if strings.Contains(q, res.substr) {
			res.used = true
			return &rows{columns: res.columns, data: res.rows}
		}
	}
	return &rows{}
}

// Connect implements driver.Connector.
func (r *Recorder) Connect(context.Context) (driver.Conn, error) { return &conn{r: r}, nil }

// Driver implements driver.Connector.
func (r *Recorder) Driver() driver.Driver { return drv{r} }

type drv struct{ r *Recorder }

func (d drv) Open(string) (driver.Conn, error) { return &conn{r: d.r}, nil }

type conn struct{ r *Recorder }

func (c *conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fakedb: Prepare unsupported")
}
func (c *conn) Close() error              { return nil }
func (c *conn) Begin() (driver.Tx, error) { return c.BeginTx(context.Background(), driver.TxOptions{}) }

func (c *conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if err := c.r.record("BEGIN", nil); err != nil {
		return nil, err
	}
	return tx{c.r}, nil
}

func (c *conn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (c *conn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.r.record(q, args); err != nil {
		return nil, err
	}
	return result{}, nil
}

func (c *conn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.r.record(q, args); err != nil {
		return nil, err
	}
	return c.r.rowsFor(q), nil
}

type tx struct{ r *Recorder }

func (t tx) Commit() error   { return t.r.record("COMMIT", nil) }
func (t tx) Rollback() error { return t.r.record("ROLLBACK", nil) }

type result struct{}

func (result) LastInsertId() (int64, error) { return 1, nil }
func (result) RowsAffected() (int64, error) { return 1, nil }

type rows struct {
	columns []string
	data    [][]driver.Value
	pos     int
}

func (r *rows) Columns() []string { return r.columns }
func (r *rows) Close() error      { return nil }
func (r *rows) Next(dest []driver.Value) error {
	if r.pos >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.pos])
	r.pos++
	return nil
}
