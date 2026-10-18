package dbal

import (
	"context"
	"database/sql"
	"slices"
	"strconv"

	"github.com/smatify/smorm/dialect"
)

type cte struct {
	name      string
	query     Expr
	recursive bool
}

type tableRef struct {
	name  string
	alias string
}

func (t tableRef) AppendSQL(w *SQLWriter) error {
	w.WriteIdent(t.name)
	if t.alias != "" {
		w.WriteString(" AS ")
		w.WriteIdent(t.alias)
	}
	return nil
}

type compound struct {
	op string
	q  *SelectQuery
}

// LockMode is a row locking strength for SELECT ... FOR.
type LockMode int

// Lock modes.
const (
	LockNone LockMode = iota
	LockForUpdate
	LockForShare
)

// SelectQuery builds a SELECT statement.
type SelectQuery struct {
	db         IDB
	ctes       []cte
	distinct   bool
	distinctOn []Expr
	columns    []Expr
	from       []Expr
	joins      []Expr
	where      whereClause
	groupBy    []Expr
	having     whereClause
	orderBy    []Expr
	limit      int
	offset     int
	lock       LockMode
	skipLocked bool
	noWait     bool
	compounds  []compound
	err        error
}

// NewSelect starts a SELECT query executed on db.
func NewSelect(db IDB) *SelectQuery { return &SelectQuery{db: db, limit: -1} }

// Clone returns a deep enough copy to modify independently.
func (q *SelectQuery) Clone() *SelectQuery {
	c := *q
	c.ctes = slices.Clone(q.ctes)
	c.distinctOn = slices.Clone(q.distinctOn)
	c.columns = slices.Clone(q.columns)
	c.from = slices.Clone(q.from)
	c.joins = slices.Clone(q.joins)
	c.where = whereClause{parts: slices.Clone(q.where.parts)}
	c.groupBy = slices.Clone(q.groupBy)
	c.having = whereClause{parts: slices.Clone(q.having.parts)}
	c.orderBy = slices.Clone(q.orderBy)
	c.compounds = slices.Clone(q.compounds)
	return &c
}

// With adds a common table expression.
func (q *SelectQuery) With(name string, query Expr) *SelectQuery {
	q.ctes = append(q.ctes, cte{name: name, query: query})
	return q
}

// WithRecursive adds a recursive common table expression.
func (q *SelectQuery) WithRecursive(name string, query Expr) *SelectQuery {
	q.ctes = append(q.ctes, cte{name: name, query: query, recursive: true})
	return q
}

// Distinct adds DISTINCT.
func (q *SelectQuery) Distinct() *SelectQuery { q.distinct = true; return q }

// DistinctOn adds DISTINCT ON (...) (PostgreSQL only).
func (q *SelectQuery) DistinctOn(query string, args ...any) *SelectQuery {
	q.distinctOn = append(q.distinctOn, SQL(query, args...))
	return q
}

// Column adds quoted columns ("u.name", "u.*").
func (q *SelectQuery) Column(names ...string) *SelectQuery {
	for _, n := range names {
		q.columns = append(q.columns, Ident(n))
	}
	return q
}

// ColumnExpr adds a column expression.
func (q *SelectQuery) ColumnExpr(query string, args ...any) *SelectQuery {
	q.columns = append(q.columns, SQL(query, args...))
	return q
}

// Table adds a FROM table.
func (q *SelectQuery) Table(name string) *SelectQuery {
	q.from = append(q.from, tableRef{name: name})
	return q
}

// TableAs adds a FROM table with alias.
func (q *SelectQuery) TableAs(name, alias string) *SelectQuery {
	q.from = append(q.from, tableRef{name: name, alias: alias})
	return q
}

// TableExpr adds a FROM expression, e.g. a sub query: TableExpr("? AS s", sub).
func (q *SelectQuery) TableExpr(query string, args ...any) *SelectQuery {
	q.from = append(q.from, SQL(query, args...))
	return q
}

// Join adds a raw join clause, e.g. Join("LEFT JOIN users AS u ON u.id = p.user_id").
func (q *SelectQuery) Join(query string, args ...any) *SelectQuery {
	q.joins = append(q.joins, SQL(query, args...))
	return q
}

func (q *SelectQuery) join(kind, table, alias, on string, args []any) *SelectQuery {
	q.joins = append(q.joins, joinExpr{kind: kind, table: tableRef{name: table, alias: alias}, on: SQL(on, args...)})
	return q
}

// InnerJoin adds INNER JOIN table AS alias ON on.
func (q *SelectQuery) InnerJoin(table, alias, on string, args ...any) *SelectQuery {
	return q.join("INNER JOIN", table, alias, on, args)
}

// LeftJoin adds LEFT JOIN table AS alias ON on.
func (q *SelectQuery) LeftJoin(table, alias, on string, args ...any) *SelectQuery {
	return q.join("LEFT JOIN", table, alias, on, args)
}

type joinExpr struct {
	kind  string
	table tableRef
	on    Expr
}

func (j joinExpr) AppendSQL(w *SQLWriter) error {
	w.WriteString(j.kind + " ")
	_ = j.table.AppendSQL(w)
	w.WriteString(" ON ")
	return j.on.AppendSQL(w)
}

// Where adds a condition joined with AND.
func (q *SelectQuery) Where(query string, args ...any) *SelectQuery {
	q.where.add(" AND ", SQL(query, args...))
	return q
}

// WhereOr adds a condition joined with OR.
func (q *SelectQuery) WhereOr(query string, args ...any) *SelectQuery {
	q.where.add(" OR ", SQL(query, args...))
	return q
}

// WhereExpr adds an expression joined with AND.
func (q *SelectQuery) WhereExpr(e Expr) *SelectQuery { q.where.add(" AND ", e); return q }

// WhereGroup adds a parenthesized group joined with AND.
func (q *SelectQuery) WhereGroup(fn func(*WhereBuilder)) *SelectQuery {
	q.where.addGroup(" AND ", fn)
	return q
}

// WhereOrGroup adds a parenthesized group joined with OR.
func (q *SelectQuery) WhereOrGroup(fn func(*WhereBuilder)) *SelectQuery {
	q.where.addGroup(" OR ", fn)
	return q
}

// GroupBy adds quoted GROUP BY columns.
func (q *SelectQuery) GroupBy(names ...string) *SelectQuery {
	for _, n := range names {
		q.groupBy = append(q.groupBy, Ident(n))
	}
	return q
}

// GroupByExpr adds a GROUP BY expression.
func (q *SelectQuery) GroupByExpr(query string, args ...any) *SelectQuery {
	q.groupBy = append(q.groupBy, SQL(query, args...))
	return q
}

// Having adds a HAVING condition joined with AND.
func (q *SelectQuery) Having(query string, args ...any) *SelectQuery {
	q.having.add(" AND ", SQL(query, args...))
	return q
}

// Order is a sort direction.
type Order string

// Sort directions.
const (
	Asc  Order = "ASC"
	Desc Order = "DESC"
)

// OrderBy adds a quoted column with direction.
func (q *SelectQuery) OrderBy(column string, dir Order) *SelectQuery {
	if dir != Desc {
		dir = Asc
	}
	q.orderBy = append(q.orderBy, SQL("? "+string(dir), Ident(column)))
	return q
}

// OrderExpr adds an ORDER BY expression.
func (q *SelectQuery) OrderExpr(query string, args ...any) *SelectQuery {
	q.orderBy = append(q.orderBy, SQL(query, args...))
	return q
}

// Limit sets the maximum number of rows; negative removes the limit.
func (q *SelectQuery) Limit(n int) *SelectQuery { q.limit = n; return q }

// Offset sets the number of rows to skip.
func (q *SelectQuery) Offset(n int) *SelectQuery { q.offset = n; return q }

// Lock adds row locking (FOR UPDATE / FOR SHARE, or table hints on SQL Server).
// SQLite ignores row locks because it locks the whole database.
func (q *SelectQuery) Lock(mode LockMode) *SelectQuery { q.lock = mode; return q }

// ForUpdate is shorthand for Lock(LockForUpdate).
func (q *SelectQuery) ForUpdate() *SelectQuery { return q.Lock(LockForUpdate) }

// SkipLocked skips rows locked by other transactions.
func (q *SelectQuery) SkipLocked() *SelectQuery { q.skipLocked = true; return q }

// NoWait fails instead of waiting for locked rows.
func (q *SelectQuery) NoWait() *SelectQuery { q.noWait = true; return q }

// Union appends UNION other.
func (q *SelectQuery) Union(other *SelectQuery) *SelectQuery { return q.compound("UNION", other) }

// UnionAll appends UNION ALL other.
func (q *SelectQuery) UnionAll(other *SelectQuery) *SelectQuery {
	return q.compound("UNION ALL", other)
}

// Intersect appends INTERSECT other.
func (q *SelectQuery) Intersect(other *SelectQuery) *SelectQuery {
	return q.compound("INTERSECT", other)
}

// Except appends EXCEPT other.
func (q *SelectQuery) Except(other *SelectQuery) *SelectQuery { return q.compound("EXCEPT", other) }

func (q *SelectQuery) compound(op string, other *SelectQuery) *SelectQuery {
	q.compounds = append(q.compounds, compound{op: op, q: other})
	return q
}

// Err records an error that is returned on build; used by higher layers.
func (q *SelectQuery) Err(err error) *SelectQuery {
	if q.err == nil {
		q.err = err
	}
	return q
}

// AppendSQL renders the query as a parenthesized sub query.
func (q *SelectQuery) AppendSQL(w *SQLWriter) error {
	w.WriteString("(")
	if err := q.AppendQuery(w); err != nil {
		return err
	}
	w.WriteString(")")
	return nil
}

// AppendQuery renders the query without surrounding parentheses.
func (q *SelectQuery) AppendQuery(w *SQLWriter) error {
	if q.err != nil {
		return q.err
	}
	if err := appendCTEs(w, q.ctes); err != nil {
		return err
	}
	if err := q.appendCore(w); err != nil {
		return err
	}
	for _, c := range q.compounds {
		w.WriteString(" " + c.op + " ")
		if c.q.err != nil {
			return c.q.err
		}
		if err := c.q.appendCore(w); err != nil {
			return err
		}
	}
	return q.appendTail(w)
}

func appendCTEs(w *SQLWriter, ctes []cte) error {
	if len(ctes) == 0 {
		return nil
	}
	w.WriteString("WITH ")
	for _, c := range ctes {
		if c.recursive && w.Dialect().Name() != dialect.MSSQL {
			w.WriteString("RECURSIVE ")
			break
		}
	}
	for i, c := range ctes {
		if i > 0 {
			w.WriteString(", ")
		}
		w.WriteIdent(c.name)
		w.WriteString(" AS (")
		var err error
		if sq, ok := c.query.(*SelectQuery); ok {
			err = sq.AppendQuery(w)
		} else {
			err = c.query.AppendSQL(w)
		}
		if err != nil {
			return err
		}
		w.WriteString(")")
	}
	w.WriteString(" ")
	return nil
}

func (q *SelectQuery) appendCore(w *SQLWriter) error {
	d := w.Dialect()
	w.WriteString("SELECT ")
	if len(q.distinctOn) > 0 {
		w.WriteString("DISTINCT ON (")
		if err := writeExprs(w, q.distinctOn); err != nil {
			return err
		}
		w.WriteString(") ")
	} else if q.distinct {
		w.WriteString("DISTINCT ")
	}
	if len(q.columns) == 0 {
		w.WriteString("*")
	} else if err := writeExprs(w, q.columns); err != nil {
		return err
	}
	if len(q.from) > 0 {
		w.WriteString(" FROM ")
		for i, f := range q.from {
			if i > 0 {
				w.WriteString(", ")
			}
			if err := f.AppendSQL(w); err != nil {
				return err
			}
			if i == 0 && d.Name() == dialect.MSSQL {
				q.appendMSSQLLockHint(w)
			}
		}
	}
	for _, j := range q.joins {
		w.WriteString(" ")
		if err := j.AppendSQL(w); err != nil {
			return err
		}
	}
	if err := q.where.appendWhere(w); err != nil {
		return err
	}
	if len(q.groupBy) > 0 {
		w.WriteString(" GROUP BY ")
		if err := writeExprs(w, q.groupBy); err != nil {
			return err
		}
	}
	if !q.having.empty() {
		w.WriteString(" HAVING ")
		if err := q.having.appendSQL(w); err != nil {
			return err
		}
	}
	return nil
}

func (q *SelectQuery) appendMSSQLLockHint(w *SQLWriter) {
	if q.lock == LockNone {
		return
	}
	w.WriteString(" WITH (")
	if q.lock == LockForUpdate {
		w.WriteString("UPDLOCK, ROWLOCK")
	} else {
		w.WriteString("HOLDLOCK, ROWLOCK")
	}
	if q.skipLocked {
		w.WriteString(", READPAST")
	}
	if q.noWait {
		w.WriteString(", NOWAIT")
	}
	w.WriteString(")")
}

func (q *SelectQuery) appendTail(w *SQLWriter) error {
	d := w.Dialect()
	f := d.Features()
	if len(q.orderBy) > 0 {
		w.WriteString(" ORDER BY ")
		if err := writeExprs(w, q.orderBy); err != nil {
			return err
		}
	}
	if q.limit >= 0 || q.offset > 0 {
		if f.Has(dialect.FeatureOffsetFetch) && !f.Has(dialect.FeatureLimitOffset) {
			if len(q.orderBy) == 0 {
				w.WriteString(" ORDER BY (SELECT NULL)")
			}
			w.WriteString(" OFFSET " + strconv.Itoa(q.offset) + " ROWS")
			if q.limit >= 0 {
				w.WriteString(" FETCH NEXT " + strconv.Itoa(q.limit) + " ROWS ONLY")
			}
		} else {
			switch {
			case q.limit >= 0:
				w.WriteString(" LIMIT " + strconv.Itoa(q.limit))
			case d.Name() == dialect.MySQL || d.Name() == dialect.MariaDB:
				w.WriteString(" LIMIT 18446744073709551615") // MySQL requires LIMIT before OFFSET
			case d.Name() == dialect.SQLite:
				w.WriteString(" LIMIT -1")
			}
			if q.offset > 0 {
				w.WriteString(" OFFSET " + strconv.Itoa(q.offset))
			}
		}
	}
	if q.lock != LockNone && f.Has(dialect.FeatureSelectForUpdate) {
		switch {
		case q.lock == LockForUpdate:
			w.WriteString(" FOR UPDATE")
		case d.Name() == dialect.MariaDB:
			w.WriteString(" LOCK IN SHARE MODE")
		default:
			w.WriteString(" FOR SHARE")
		}
		if q.skipLocked && f.Has(dialect.FeatureSkipLocked) {
			w.WriteString(" SKIP LOCKED")
		}
		if q.noWait {
			w.WriteString(" NOWAIT")
		}
	}
	return nil
}

// Build renders the query.
func (q *SelectQuery) Build() (string, []any, error) {
	w := NewSQLWriter(q.db.Dialect())
	if err := q.AppendQuery(w); err != nil {
		return "", nil, err
	}
	return w.String(), w.Args(), nil
}

// String renders the query for debugging; errors are rendered inline.
func (q *SelectQuery) String() string {
	s, _, err := q.Build()
	if err != nil {
		return "error: " + err.Error()
	}
	return s
}

// Rows executes the query.
func (q *SelectQuery) Rows(ctx context.Context) (*sql.Rows, error) {
	s, args, err := q.Build()
	if err != nil {
		return nil, err
	}
	return q.db.QueryContext(ctx, s, args...)
}

// Scan executes the query and scans the first row into dest.
// It returns sql.ErrNoRows when there is no row.
func (q *SelectQuery) Scan(ctx context.Context, dest ...any) error {
	s, args, err := q.Build()
	if err != nil {
		return err
	}
	return q.db.QueryRowContext(ctx, s, args...).Scan(dest...)
}

// ForEach executes the query and calls fn for every row.
func (q *SelectQuery) ForEach(ctx context.Context, fn func(rows *sql.Rows) error) error {
	rows, err := q.Rows(ctx)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanMaps executes the query and returns every row as a column map.
func (q *SelectQuery) ScanMaps(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	var cols []string
	err := q.ForEach(ctx, func(rows *sql.Rows) error {
		if cols == nil {
			var err error
			if cols, err = rows.Columns(); err != nil {
				return err
			}
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = slices.Clone(b)
			}
			m[c] = vals[i]
		}
		out = append(out, m)
		return nil
	})
	return out, err
}

// CountQuery returns a query counting the rows q would return, ignoring
// ORDER BY, LIMIT and OFFSET.
func (q *SelectQuery) CountQuery() *SelectQuery {
	inner := q.Clone()
	inner.orderBy, inner.limit, inner.offset, inner.lock = nil, -1, 0, LockNone
	if !inner.distinct && len(inner.distinctOn) == 0 && len(inner.groupBy) == 0 && len(inner.compounds) == 0 {
		inner.columns = []Expr{Safe("COUNT(*)")}
		return inner
	}
	ctes := inner.ctes
	inner.ctes = nil
	return &SelectQuery{db: q.db, limit: -1, ctes: ctes,
		columns: []Expr{Safe("COUNT(*)")}, from: []Expr{SQL("? AS dbal_count", inner)}}
}

// Count returns the number of rows the query would return.
func (q *SelectQuery) Count(ctx context.Context) (int, error) {
	var n int
	err := q.CountQuery().Scan(ctx, &n)
	return n, err
}

// Exists reports whether the query returns at least one row.
func (q *SelectQuery) Exists(ctx context.Context) (bool, error) {
	inner := q.Clone()
	inner.orderBy = nil
	var n int
	err := (&SelectQuery{db: q.db, limit: -1, columns: []Expr{SQL("CASE WHEN EXISTS ? THEN 1 ELSE 0 END", inner)}}).Scan(ctx, &n)
	return n == 1, err
}
