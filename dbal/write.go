package dbal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/smatify/smorm/dialect"
)

var (
	// ErrReturningUnsupported is returned when RETURNING is requested on a
	// dialect that cannot return rows from this statement.
	ErrReturningUnsupported = errors.New("dbal: RETURNING is not supported by this dialect")
	// ErrNoWhere protects against accidental full-table UPDATE/DELETE.
	ErrNoWhere = errors.New("dbal: UPDATE/DELETE without WHERE; call AllRows() to confirm")
	// ErrNoValues is returned for INSERT without rows.
	ErrNoValues = errors.New("dbal: INSERT without values")
)

// writeQuery holds what INSERT, UPDATE and DELETE share.
type writeQuery struct {
	db        IDB
	ctes      []cte
	table     tableRef
	returning []string
	err       error
}

func isMySQLFamily(d dialect.Dialect) bool {
	return d.Name() == dialect.MySQL || d.Name() == dialect.MariaDB
}

// appendOutput writes the SQL Server OUTPUT clause.
func appendOutput(w *SQLWriter, prefix string, cols []string) {
	w.WriteString(" OUTPUT ")
	for i, c := range cols {
		if i > 0 {
			w.WriteString(", ")
		}
		w.WriteString(prefix + ".")
		w.WriteIdent(c)
	}
}

func appendReturning(w *SQLWriter, cols []string) {
	w.WriteString(" RETURNING ")
	writeIdents(w, cols)
}

func build(d dialect.Dialect, appendFn func(*SQLWriter) error) (string, []any, error) {
	w := NewSQLWriter(d)
	if err := appendFn(w); err != nil {
		return "", nil, err
	}
	return w.String(), w.Args(), nil
}

func execQuery(ctx context.Context, db IDB, appendFn func(*SQLWriter) error) (sql.Result, error) {
	s, args, err := build(db.Dialect(), appendFn)
	if err != nil {
		return nil, err
	}
	return db.ExecContext(ctx, s, args...)
}

func queryRows(ctx context.Context, db IDB, appendFn func(*SQLWriter) error) (*sql.Rows, error) {
	s, args, err := build(db.Dialect(), appendFn)
	if err != nil {
		return nil, err
	}
	return db.QueryContext(ctx, s, args...)
}

func scanRow(ctx context.Context, db IDB, appendFn func(*SQLWriter) error, dest []any) error {
	s, args, err := build(db.Dialect(), appendFn)
	if err != nil {
		return err
	}
	return db.QueryRowContext(ctx, s, args...).Scan(dest...)
}

// ---------------------------------------------------------------- INSERT

type conflictAction int

const (
	conflictNone conflictAction = iota
	conflictNothing
	conflictUpdate
)

type setClause struct {
	column string
	value  Expr
}

// InsertQuery builds an INSERT statement, optionally as an upsert.
type InsertQuery struct {
	writeQuery
	columns        []string
	rows           [][]any
	selectQuery    *SelectQuery
	conflictTarget []string
	conflict       conflictAction
	updates        []setClause
}

// NewInsert starts an INSERT query executed on db.
func NewInsert(db IDB) *InsertQuery { return &InsertQuery{writeQuery: writeQuery{db: db}} }

// With adds a common table expression.
func (q *InsertQuery) With(name string, query Expr) *InsertQuery {
	q.ctes = append(q.ctes, cte{name: name, query: query})
	return q
}

// Table sets the target table.
func (q *InsertQuery) Table(name string) *InsertQuery { q.table = tableRef{name: name}; return q }

// Columns sets the inserted columns.
func (q *InsertQuery) Columns(cols ...string) *InsertQuery { q.columns = cols; return q }

// Values adds a row; call it multiple times for a multi-row insert. Values
// may be Expr (e.g. Safe("DEFAULT")).
func (q *InsertQuery) Values(vals ...any) *InsertQuery {
	if len(q.columns) > 0 && len(vals) != len(q.columns) {
		q.setErr(fmt.Errorf("dbal: insert row has %d values for %d columns", len(vals), len(q.columns)))
	}
	q.rows = append(q.rows, vals)
	return q
}

// Select inserts the rows returned by sub (INSERT ... SELECT).
func (q *InsertQuery) Select(sub *SelectQuery) *InsertQuery { q.selectQuery = sub; return q }

// OnConflict sets the unique columns whose conflict triggers the upsert action.
// MySQL/MariaDB detect conflicts on any unique key and ignore the target.
func (q *InsertQuery) OnConflict(target ...string) *InsertQuery { q.conflictTarget = target; return q }

// DoNothing keeps the existing row on conflict.
func (q *InsertQuery) DoNothing() *InsertQuery { q.conflict = conflictNothing; return q }

// DoUpdate overwrites cols with the values proposed for insertion.
func (q *InsertQuery) DoUpdate(cols ...string) *InsertQuery {
	q.conflict = conflictUpdate
	for _, c := range cols {
		q.updates = append(q.updates, setClause{column: c, value: Excluded(c)})
	}
	return q
}

// DoUpdateSet sets col to an expression on conflict. Use Excluded(col) to
// reference the proposed value.
func (q *InsertQuery) DoUpdateSet(col, query string, args ...any) *InsertQuery {
	q.conflict = conflictUpdate
	q.updates = append(q.updates, setClause{column: col, value: SQL(query, args...)})
	return q
}

// Returning requests columns of the inserted rows ("*" for all).
func (q *InsertQuery) Returning(cols ...string) *InsertQuery { q.returning = cols; return q }

func (q *InsertQuery) setErr(err error) {
	if q.err == nil {
		q.err = err
	}
}

// ExcludedExpr references the value proposed for insertion in an upsert.
type ExcludedExpr string

// Excluded references the proposed value of col in DoUpdateSet.
func Excluded(col string) ExcludedExpr { return ExcludedExpr(col) }

// AppendSQL implements Expr.
func (e ExcludedExpr) AppendSQL(w *SQLWriter) error {
	switch w.Dialect().Name() {
	case dialect.MySQL:
		w.WriteString("new.")
		w.WriteIdent(string(e))
	case dialect.MariaDB:
		w.WriteString("VALUES(")
		w.WriteIdent(string(e))
		w.WriteString(")")
	case dialect.MSSQL:
		w.WriteString("source.")
		w.WriteIdent(string(e))
	default:
		w.WriteString("EXCLUDED.")
		w.WriteIdent(string(e))
	}
	return nil
}

// AppendQuery renders the statement.
func (q *InsertQuery) AppendQuery(w *SQLWriter) error {
	if q.err != nil {
		return q.err
	}
	if len(q.rows) == 0 && q.selectQuery == nil {
		return ErrNoValues
	}
	d := w.Dialect()
	if len(q.returning) > 0 && !d.Features().Has(dialect.FeatureInsertReturning) && !d.Features().Has(dialect.FeatureOutput) {
		return ErrReturningUnsupported
	}
	if q.conflict != conflictNone && d.Name() == dialect.MSSQL {
		return q.appendMerge(w)
	}
	if err := appendCTEs(w, q.ctes); err != nil {
		return err
	}
	w.WriteString("INSERT INTO ")
	_ = q.table.AppendSQL(w)
	if len(q.columns) > 0 {
		w.WriteString(" (")
		writeIdents(w, q.columns)
		w.WriteString(")")
	}
	if len(q.returning) > 0 && d.Name() == dialect.MSSQL {
		appendOutput(w, "INSERTED", q.returning)
	}
	if q.selectQuery != nil {
		w.WriteString(" ")
		if err := q.selectQuery.AppendQuery(w); err != nil {
			return err
		}
	} else {
		w.WriteString(" ")
		if err := q.appendValues(w); err != nil {
			return err
		}
	}
	if err := q.appendConflict(w); err != nil {
		return err
	}
	if len(q.returning) > 0 && d.Name() != dialect.MSSQL {
		appendReturning(w, q.returning)
	}
	return nil
}

func (q *InsertQuery) appendValues(w *SQLWriter) error {
	w.WriteString("VALUES ")
	for i, row := range q.rows {
		if i > 0 {
			w.WriteString(", ")
		}
		w.WriteString("(")
		for j, v := range row {
			if j > 0 {
				w.WriteString(", ")
			}
			if err := w.WriteArg(v); err != nil {
				return err
			}
		}
		w.WriteString(")")
	}
	return nil
}

func (q *InsertQuery) appendConflict(w *SQLWriter) error {
	if q.conflict == conflictNone {
		return nil
	}
	d := w.Dialect()
	if isMySQLFamily(d) {
		if d.Name() == dialect.MySQL && q.selectQuery == nil && q.conflict == conflictUpdate {
			w.WriteString(" AS new")
		}
		w.WriteString(" ON DUPLICATE KEY UPDATE ")
		if q.conflict == conflictNothing {
			col := firstNonEmpty(q.conflictTarget, q.columns)
			if col == "" {
				return errors.New("dbal: DoNothing on MySQL needs a conflict target or columns")
			}
			w.WriteIdent(col)
			w.WriteString(" = ")
			w.WriteIdent(col)
			return nil
		}
		return appendSets(w, q.updates)
	}
	w.WriteString(" ON CONFLICT")
	if len(q.conflictTarget) > 0 {
		w.WriteString(" (")
		writeIdents(w, q.conflictTarget)
		w.WriteString(")")
	} else if q.conflict == conflictUpdate {
		return errors.New("dbal: DoUpdate requires OnConflict target columns")
	}
	if q.conflict == conflictNothing {
		w.WriteString(" DO NOTHING")
		return nil
	}
	w.WriteString(" DO UPDATE SET ")
	return appendSets(w, q.updates)
}

func firstNonEmpty(lists ...[]string) string {
	for _, l := range lists {
		if len(l) > 0 {
			return l[0]
		}
	}
	return ""
}

func appendSets(w *SQLWriter, sets []setClause) error {
	for i, s := range sets {
		if i > 0 {
			w.WriteString(", ")
		}
		w.WriteIdent(s.column)
		w.WriteString(" = ")
		if err := s.value.AppendSQL(w); err != nil {
			return err
		}
	}
	return nil
}

// appendMerge renders an upsert as a SQL Server MERGE statement.
func (q *InsertQuery) appendMerge(w *SQLWriter) error {
	if len(q.conflictTarget) == 0 || len(q.columns) == 0 || q.selectQuery != nil {
		return errors.New("dbal: upsert on SQL Server needs OnConflict target, Columns and Values")
	}
	if err := appendCTEs(w, q.ctes); err != nil {
		return err
	}
	w.WriteString("MERGE INTO ")
	w.WriteIdent(q.table.name)
	w.WriteString(" WITH (HOLDLOCK) AS target USING (")
	if err := q.appendValues(w); err != nil {
		return err
	}
	w.WriteString(") AS source (")
	writeIdents(w, q.columns)
	w.WriteString(") ON (")
	for i, c := range q.conflictTarget {
		if i > 0 {
			w.WriteString(" AND ")
		}
		w.WriteString("target.")
		w.WriteIdent(c)
		w.WriteString(" = source.")
		w.WriteIdent(c)
	}
	w.WriteString(")")
	if q.conflict == conflictUpdate {
		w.WriteString(" WHEN MATCHED THEN UPDATE SET ")
		for i, s := range q.updates {
			if i > 0 {
				w.WriteString(", ")
			}
			w.WriteString("target.")
			w.WriteIdent(s.column)
			w.WriteString(" = ")
			if err := s.value.AppendSQL(w); err != nil {
				return err
			}
		}
	}
	w.WriteString(" WHEN NOT MATCHED THEN INSERT (")
	writeIdents(w, q.columns)
	w.WriteString(") VALUES (")
	for i, c := range q.columns {
		if i > 0 {
			w.WriteString(", ")
		}
		w.WriteString("source.")
		w.WriteIdent(c)
	}
	w.WriteString(")")
	if len(q.returning) > 0 {
		appendOutput(w, "INSERTED", q.returning)
	}
	w.WriteString(";")
	return nil
}

// Build renders the statement.
func (q *InsertQuery) Build() (string, []any, error) { return build(q.db.Dialect(), q.AppendQuery) }

// String renders the statement for debugging.
func (q *InsertQuery) String() string { return debugString(q.Build()) }

// Exec executes the statement.
func (q *InsertQuery) Exec(ctx context.Context) (sql.Result, error) {
	return execQuery(ctx, q.db, q.AppendQuery)
}

// Rows executes the statement and returns the RETURNING rows.
func (q *InsertQuery) Rows(ctx context.Context) (*sql.Rows, error) {
	return queryRows(ctx, q.db, q.AppendQuery)
}

// Scan executes the statement and scans the first RETURNING row.
func (q *InsertQuery) Scan(ctx context.Context, dest ...any) error {
	return scanRow(ctx, q.db, q.AppendQuery, dest)
}

// ---------------------------------------------------------------- UPDATE

// UpdateQuery builds an UPDATE statement.
type UpdateQuery struct {
	writeQuery
	sets    []setClause
	raw     []Expr
	from    []Expr
	where   whereClause
	allRows bool
}

// NewUpdate starts an UPDATE query executed on db.
func NewUpdate(db IDB) *UpdateQuery { return &UpdateQuery{writeQuery: writeQuery{db: db}} }

// With adds a common table expression.
func (q *UpdateQuery) With(name string, query Expr) *UpdateQuery {
	q.ctes = append(q.ctes, cte{name: name, query: query})
	return q
}

// Table sets the target table.
func (q *UpdateQuery) Table(name string) *UpdateQuery { q.table = tableRef{name: name}; return q }

// TableAs sets the target table with alias.
func (q *UpdateQuery) TableAs(name, alias string) *UpdateQuery {
	q.table = tableRef{name: name, alias: alias}
	return q
}

// Set assigns value (bound, or rendered if it is an Expr) to col.
func (q *UpdateQuery) Set(col string, value any) *UpdateQuery {
	e, ok := value.(Expr)
	if !ok {
		e = SQL("?", value)
	}
	q.sets = append(q.sets, setClause{column: col, value: e})
	return q
}

// SetExpr adds a raw assignment, e.g. SetExpr("hits = hits + ?", 1).
func (q *UpdateQuery) SetExpr(query string, args ...any) *UpdateQuery {
	q.raw = append(q.raw, SQL(query, args...))
	return q
}

// From adds a source of the new values, e.g. From("? AS v", sub)
// (UPDATE … FROM; Postgres and SQLite only).
func (q *UpdateQuery) From(query string, args ...any) *UpdateQuery {
	q.from = append(q.from, SQL(query, args...))
	return q
}

// Where adds a condition joined with AND.
func (q *UpdateQuery) Where(query string, args ...any) *UpdateQuery {
	q.where.add(" AND ", SQL(query, args...))
	return q
}

// WhereOr adds a condition joined with OR.
func (q *UpdateQuery) WhereOr(query string, args ...any) *UpdateQuery {
	q.where.add(" OR ", SQL(query, args...))
	return q
}

// WhereExpr adds an expression joined with AND.
func (q *UpdateQuery) WhereExpr(e Expr) *UpdateQuery { q.where.add(" AND ", e); return q }

// WhereGroup adds a parenthesized group joined with AND.
func (q *UpdateQuery) WhereGroup(fn func(*WhereBuilder)) *UpdateQuery {
	q.where.addGroup(" AND ", fn)
	return q
}

// AllRows confirms an UPDATE without WHERE.
func (q *UpdateQuery) AllRows() *UpdateQuery { q.allRows = true; return q }

// Returning requests columns of the updated rows.
func (q *UpdateQuery) Returning(cols ...string) *UpdateQuery { q.returning = cols; return q }

// AppendQuery renders the statement.
func (q *UpdateQuery) AppendQuery(w *SQLWriter) error {
	if q.err != nil {
		return q.err
	}
	d := w.Dialect()
	if q.where.empty() && !q.allRows {
		return ErrNoWhere
	}
	if len(q.sets)+len(q.raw) == 0 {
		return errors.New("dbal: UPDATE without SET")
	}
	mssql := d.Name() == dialect.MSSQL
	if len(q.returning) > 0 && !d.Features().Has(dialect.FeatureReturning) && !mssql {
		return ErrReturningUnsupported
	}
	if err := appendCTEs(w, q.ctes); err != nil {
		return err
	}
	w.WriteString("UPDATE ")
	if mssql && q.table.alias != "" {
		w.WriteIdent(q.table.alias)
	} else {
		_ = q.table.AppendSQL(w)
	}
	w.WriteString(" SET ")
	if err := appendSets(w, q.sets); err != nil {
		return err
	}
	if len(q.sets) > 0 && len(q.raw) > 0 {
		w.WriteString(", ")
	}
	if err := writeExprs(w, q.raw); err != nil {
		return err
	}
	if len(q.from) > 0 {
		if d.Name() != dialect.Postgres && d.Name() != dialect.SQLite {
			return fmt.Errorf("dbal: UPDATE … FROM is not supported by %s", d.Name())
		}
		w.WriteString(" FROM ")
		if err := writeExprs(w, q.from); err != nil {
			return err
		}
	}
	if mssql {
		if len(q.returning) > 0 {
			appendOutput(w, "INSERTED", q.returning)
		}
		if q.table.alias != "" {
			w.WriteString(" FROM ")
			_ = q.table.AppendSQL(w)
		}
	}
	if err := q.where.appendWhere(w); err != nil {
		return err
	}
	if len(q.returning) > 0 && !mssql {
		appendReturning(w, q.returning)
	}
	return nil
}

// Build renders the statement.
func (q *UpdateQuery) Build() (string, []any, error) { return build(q.db.Dialect(), q.AppendQuery) }

// String renders the statement for debugging.
func (q *UpdateQuery) String() string { return debugString(q.Build()) }

// Exec executes the statement.
func (q *UpdateQuery) Exec(ctx context.Context) (sql.Result, error) {
	return execQuery(ctx, q.db, q.AppendQuery)
}

// Rows executes the statement and returns the RETURNING rows.
func (q *UpdateQuery) Rows(ctx context.Context) (*sql.Rows, error) {
	return queryRows(ctx, q.db, q.AppendQuery)
}

// Scan executes the statement and scans the first RETURNING row.
func (q *UpdateQuery) Scan(ctx context.Context, dest ...any) error {
	return scanRow(ctx, q.db, q.AppendQuery, dest)
}

// ---------------------------------------------------------------- DELETE

// DeleteQuery builds a DELETE statement.
type DeleteQuery struct {
	writeQuery
	where   whereClause
	allRows bool
}

// NewDelete starts a DELETE query executed on db.
func NewDelete(db IDB) *DeleteQuery { return &DeleteQuery{writeQuery: writeQuery{db: db}} }

// With adds a common table expression.
func (q *DeleteQuery) With(name string, query Expr) *DeleteQuery {
	q.ctes = append(q.ctes, cte{name: name, query: query})
	return q
}

// Table sets the target table.
func (q *DeleteQuery) Table(name string) *DeleteQuery { q.table = tableRef{name: name}; return q }

// TableAs sets the target table with alias.
func (q *DeleteQuery) TableAs(name, alias string) *DeleteQuery {
	q.table = tableRef{name: name, alias: alias}
	return q
}

// Where adds a condition joined with AND.
func (q *DeleteQuery) Where(query string, args ...any) *DeleteQuery {
	q.where.add(" AND ", SQL(query, args...))
	return q
}

// WhereOr adds a condition joined with OR.
func (q *DeleteQuery) WhereOr(query string, args ...any) *DeleteQuery {
	q.where.add(" OR ", SQL(query, args...))
	return q
}

// WhereExpr adds an expression joined with AND.
func (q *DeleteQuery) WhereExpr(e Expr) *DeleteQuery { q.where.add(" AND ", e); return q }

// WhereGroup adds a parenthesized group joined with AND.
func (q *DeleteQuery) WhereGroup(fn func(*WhereBuilder)) *DeleteQuery {
	q.where.addGroup(" AND ", fn)
	return q
}

// AllRows confirms a DELETE without WHERE.
func (q *DeleteQuery) AllRows() *DeleteQuery { q.allRows = true; return q }

// Returning requests columns of the deleted rows.
func (q *DeleteQuery) Returning(cols ...string) *DeleteQuery { q.returning = cols; return q }

// AppendQuery renders the statement.
func (q *DeleteQuery) AppendQuery(w *SQLWriter) error {
	if q.err != nil {
		return q.err
	}
	d := w.Dialect()
	if q.where.empty() && !q.allRows {
		return ErrNoWhere
	}
	mssql := d.Name() == dialect.MSSQL
	// MariaDB supports DELETE ... RETURNING although it lacks UPDATE ... RETURNING.
	canReturn := d.Features().Has(dialect.FeatureReturning) || d.Name() == dialect.MariaDB || mssql
	if len(q.returning) > 0 && (!canReturn || (d.Name() == dialect.MariaDB && q.table.alias != "")) {
		return ErrReturningUnsupported
	}
	if err := appendCTEs(w, q.ctes); err != nil {
		return err
	}
	w.WriteString("DELETE ")
	if q.table.alias != "" && (mssql || isMySQLFamily(d)) {
		// Multi-table syntax: DELETE alias FROM table AS alias.
		w.WriteIdent(q.table.alias)
		if mssql && len(q.returning) > 0 {
			appendOutput(w, "DELETED", q.returning)
		}
		w.WriteString(" FROM ")
		_ = q.table.AppendSQL(w)
	} else {
		w.WriteString("FROM ")
		_ = q.table.AppendSQL(w)
		if mssql && len(q.returning) > 0 {
			appendOutput(w, "DELETED", q.returning)
		}
	}
	if err := q.where.appendWhere(w); err != nil {
		return err
	}
	if len(q.returning) > 0 && !mssql {
		appendReturning(w, q.returning)
	}
	return nil
}

// Build renders the statement.
func (q *DeleteQuery) Build() (string, []any, error) { return build(q.db.Dialect(), q.AppendQuery) }

// String renders the statement for debugging.
func (q *DeleteQuery) String() string { return debugString(q.Build()) }

// Exec executes the statement.
func (q *DeleteQuery) Exec(ctx context.Context) (sql.Result, error) {
	return execQuery(ctx, q.db, q.AppendQuery)
}

// Rows executes the statement and returns the RETURNING rows.
func (q *DeleteQuery) Rows(ctx context.Context) (*sql.Rows, error) {
	return queryRows(ctx, q.db, q.AppendQuery)
}

// Scan executes the statement and scans the first RETURNING row.
func (q *DeleteQuery) Scan(ctx context.Context, dest ...any) error {
	return scanRow(ctx, q.db, q.AppendQuery, dest)
}

func debugString(s string, _ []any, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	return s
}
