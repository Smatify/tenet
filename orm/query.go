package orm

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect"
	"github.com/smatify/tenet/mapping"
)

// RootAlias is the alias of the queried entity.
const RootAlias = "t0"

// Query selects entities using field and association names instead of
// columns. Build it with EntityManager.NewQuery.
type Query struct {
	em      *EntityManager
	meta    *mapping.Entity
	joins   []queryJoin
	where   []Cond
	order   []queryOrder
	limit   int
	offset  int
	with    []string
	lock    dbal.LockMode
	err     error
	aliases map[string]*mapping.Entity
	without map[string]bool // filters disabled for this query
	// columns and groupBy are SQL templates of a projection (Columns).
	columns  []rawCond
	groupBy  []string
	distinct bool
	root     string // alias of the queried entity (RootAlias, As)
}

// queryJoin joins an association (assoc), an entity on a condition
// (target, on) or a sub query (sub, on or lateral).
type queryJoin struct {
	from, path, alias string
	left              bool
	assoc             *mapping.Association
	target            *mapping.Entity
	on                Cond
	sub               dbal.Expr
	lateral           bool
}

type queryOrder struct {
	field any
	dir   dbal.Order
}

// NewQuery starts a query for the entity type t (alias "t0").
func (em *EntityManager) NewQuery(t reflect.Type) *Query {
	q := &Query{em: em, limit: -1, aliases: map[string]*mapping.Entity{}}
	meta, err := em.reg.Get(t)
	if err != nil {
		q.err = err
		return q
	}
	q.meta = meta
	q.root = RootAlias
	q.aliases[RootAlias] = meta
	return q
}

// As names the queried entity alias instead of "t0", so a sub query can
// refer to the query around it, e.g. Raw("{User} = w.user_id") inside a
// query As("w").
func (q *Query) As(alias string) *Query {
	if q.err != nil {
		return q
	}
	if _, dup := q.aliases[alias]; dup || alias == "" {
		return q.fail(fmt.Errorf("query: alias %q is empty or already used", alias))
	}
	q.aliases[alias] = q.meta
	delete(q.aliases, q.root)
	for i := range q.joins {
		if q.joins[i].from == q.root {
			q.joins[i].from = alias
		}
	}
	q.root = alias
	return q
}

// Meta returns the metadata of the queried entity.
func (q *Query) Meta() *mapping.Entity { return q.meta }

func (q *Query) fail(err error) *Query {
	if q.err == nil {
		q.err = err
	}
	return q
}

// Join adds an INNER JOIN over an association. path is "Assoc" (from the
// root), "alias.Assoc" or a mapping.FieldRef of an entity in the query; the
// joined entity is reachable as alias.
func (q *Query) Join(path any, alias string) *Query { return q.join(path, alias, false) }

// LeftJoin adds a LEFT JOIN over an association.
func (q *Query) LeftJoin(path any, alias string) *Query { return q.join(path, alias, true) }

func (q *Query) join(ref any, alias string, left bool) *Query {
	if q.err != nil {
		return q
	}
	var from, name, path string
	switch r := ref.(type) {
	case string:
		from, name, path = q.root, r, r
		if i := strings.IndexByte(r, '.'); i >= 0 {
			from, name = r[:i], r[i+1:]
		}
	case mapping.FieldRef:
		a, err := q.aliasOf(r)
		if err != nil {
			return q.fail(err)
		}
		from, name, path = a, r.Name, a+"."+r.Name
	default:
		return q.fail(fmt.Errorf("query: unsupported join path %T", ref))
	}
	src, ok := q.aliases[from]
	if !ok {
		return q.fail(fmt.Errorf("query: unknown alias %q in join %q", from, path))
	}
	a := src.Assoc(name)
	if a == nil {
		return q.fail(fmt.Errorf("query: %s has no association %q", src.Name, name))
	}
	if _, dup := q.aliases[alias]; dup || alias == "" {
		return q.fail(fmt.Errorf("query: alias %q is empty or already used", alias))
	}
	q.aliases[alias] = a.Target
	q.joins = append(q.joins, queryJoin{from: from, path: name, alias: alias, left: left, assoc: a})
	return q
}

// JoinOn adds an INNER JOIN of the entity type t as alias on a condition,
// e.g. JoinOn(commentType, "c", Raw("{c.ObjectID} = {ID}")); the filters
// of t become part of the ON clause.
func (q *Query) JoinOn(t reflect.Type, alias string, on Cond) *Query {
	return q.joinOn(t, alias, on, false)
}

// LeftJoinOn adds a LEFT JOIN of the entity type t as alias on a condition.
func (q *Query) LeftJoinOn(t reflect.Type, alias string, on Cond) *Query {
	return q.joinOn(t, alias, on, true)
}

func (q *Query) joinOn(t reflect.Type, alias string, on Cond, left bool) *Query {
	if q.err != nil {
		return q
	}
	meta, err := q.em.reg.Get(t)
	if err != nil {
		return q.fail(err)
	}
	if _, dup := q.aliases[alias]; dup || alias == "" {
		return q.fail(fmt.Errorf("query: alias %q is empty or already used", alias))
	}
	q.aliases[alias] = meta
	q.joins = append(q.joins, queryJoin{alias: alias, left: left, target: meta, on: on})
	return q
}

// JoinSub adds an INNER JOIN of a sub query (Sub) as alias on a
// condition; its columns are plain SQL (alias.column).
func (q *Query) JoinSub(sub dbal.Expr, alias string, on Cond) *Query {
	return q.joinSub(queryJoin{sub: sub, alias: alias, on: on})
}

// LeftJoinSub adds a LEFT JOIN of a sub query as alias on a condition.
func (q *Query) LeftJoinSub(sub dbal.Expr, alias string, on Cond) *Query {
	return q.joinSub(queryJoin{sub: sub, alias: alias, on: on, left: true})
}

// JoinLateral adds a sub query run for each row (JOIN LATERAL, Postgres
// only), e.g. the last post of each thread; rows without a result drop.
// The sub query refers to the query around it as plain SQL (As).
func (q *Query) JoinLateral(sub dbal.Expr, alias string) *Query {
	return q.joinSub(queryJoin{sub: sub, alias: alias, lateral: true})
}

// LeftJoinLateral adds a sub query run for each row, keeping rows without
// a result (LEFT JOIN LATERAL … ON true, Postgres only).
func (q *Query) LeftJoinLateral(sub dbal.Expr, alias string) *Query {
	return q.joinSub(queryJoin{sub: sub, alias: alias, lateral: true, left: true})
}

func (q *Query) joinSub(j queryJoin) *Query {
	if q.err != nil {
		return q
	}
	if _, dup := q.aliases[j.alias]; dup || j.alias == "" || q.subAlias(j.alias) {
		return q.fail(fmt.Errorf("query: alias %q is empty or already used", j.alias))
	}
	q.joins = append(q.joins, j)
	return q
}

// subAlias reports whether a sub query joins as alias.
func (q *Query) subAlias(alias string) bool {
	for _, j := range q.joins {
		if j.sub != nil && j.alias == alias {
			return true
		}
	}
	return false
}

// Columns selects SQL expressions instead of entities, with {Field} or
// {alias.Field} for columns, e.g. Columns("{ID} AS id", "count({c.ID}) AS
// n"); use the query through Sub or Rows.
func (q *Query) Columns(exprs ...string) *Query {
	for _, e := range exprs {
		q.columns = append(q.columns, rawCond{sql: e})
	}
	return q
}

// Column selects one SQL expression with ? arguments, e.g.
// Column("? AS kind", "forum").
func (q *Query) Column(sql string, args ...any) *Query {
	q.columns = append(q.columns, rawCond{sql, args})
	return q
}

// Distinct removes duplicate rows of a projection (Columns).
func (q *Query) Distinct() *Query { q.distinct = true; return q }

// GroupBy groups a projection (Columns) by SQL expressions, e.g. "{ID}".
func (q *Query) GroupBy(exprs ...string) *Query { q.groupBy = append(q.groupBy, exprs...); return q }

// Sub returns the query as a parenthesized sub query, for a FROM, a join,
// "IN ?" or "EXISTS ?"; an error of the query shows when it is rendered.
func (q *Query) Sub() dbal.Expr {
	return subQuery{q}
}

type subQuery struct{ q *Query }

func (s subQuery) AppendSQL(w *dbal.SQLWriter) error {
	sel, err := s.q.build(false)
	if err != nil {
		return err
	}
	return sel.AppendSQL(w)
}

// template renders a SQL template with {Field} placeholders.
func (q *Query) template(sql string, args ...any) (dbal.Expr, error) {
	return rawCond{sql, args}.build(q)
}

// Where adds conditions joined with AND.
func (q *Query) Where(conds ...Cond) *Query { q.where = append(q.where, conds...); return q }

// OrderBy sorts by a field ("Name", "a.Name" or a mapping.FieldRef) or by an
// expression given as Raw (e.g. Raw("COALESCE({a.Position}, {Position})")).
func (q *Query) OrderBy(field any, dir dbal.Order) *Query {
	q.order = append(q.order, queryOrder{field: field, dir: dir})
	return q
}

// Limit sets the maximum number of results.
func (q *Query) Limit(n int) *Query { q.limit = n; return q }

// Offset skips results.
func (q *Query) Offset(n int) *Query { q.offset = n; return q }

// With preloads association paths of the results (see EntityManager.Preload):
// "Assoc", "Assoc.Nested" or a mapping.FieldRef of the root entity.
func (q *Query) With(paths ...any) *Query {
	if q.err != nil {
		return q
	}
	for _, p := range paths {
		switch r := p.(type) {
		case string:
			q.with = append(q.with, r)
		case mapping.FieldRef:
			if r.Entity != q.meta.Name {
				return q.fail(fmt.Errorf("query: With(%s.%s) on a query of %s", r.Entity, r.Name, q.meta.Name))
			}
			q.with = append(q.with, r.Name)
		default:
			return q.fail(fmt.Errorf("query: unsupported preload path %T", p))
		}
	}
	return q
}

// WithoutFilter disables the named filters for this query only (its root
// and joined entities; preloads keep the filters of the entity manager).
func (q *Query) WithoutFilter(names ...string) *Query {
	if q.without == nil {
		q.without = map[string]bool{}
	}
	for _, n := range names {
		q.without[n] = true
	}
	return q
}

// WithDeleted includes soft-deleted rows in this query.
func (q *Query) WithDeleted() *Query { return q.WithoutFilter(SoftDeleteFilterName) }

// ForUpdate locks the selected rows.
func (q *Query) ForUpdate() *Query { q.lock = dbal.LockForUpdate; return q }

// Clone returns an independent copy.
func (q *Query) Clone() *Query {
	c := *q
	c.joins = append([]queryJoin(nil), q.joins...)
	c.where = append([]Cond(nil), q.where...)
	c.order = append([]queryOrder(nil), q.order...)
	c.with = append([]string(nil), q.with...)
	c.columns = append([]rawCond(nil), q.columns...)
	c.groupBy = append([]string(nil), q.groupBy...)
	c.without = make(map[string]bool, len(q.without))
	for k, v := range q.without {
		c.without[k] = v
	}
	c.aliases = make(map[string]*mapping.Entity, len(q.aliases))
	for k, v := range q.aliases {
		c.aliases[k] = v
	}
	return &c
}

func (q *Query) hasToManyJoin() bool {
	for _, j := range q.joins {
		if j.assoc == nil || !j.assoc.Kind.IsToOne() {
			return true
		}
	}
	return false
}

// resolve maps a field reference to "alias.column" and its metadata.
func (q *Query) resolve(ref any) (string, *mapping.Field, error) {
	var alias, name string
	switch r := ref.(type) {
	case string:
		alias, name = q.root, r
		// "alias.Field" or a path into an embeddable ("Billing.City").
		if i := strings.IndexByte(r, '.'); i >= 0 {
			if _, ok := q.aliases[r[:i]]; ok {
				alias, name = r[:i], r[i+1:]
			}
		}
	case mapping.FieldRef:
		a, err := q.aliasOf(r)
		if err != nil {
			return "", nil, err
		}
		alias = a
		name = r.Name
	default:
		return "", nil, fmt.Errorf("query: unsupported field reference %T", ref)
	}
	m, ok := q.aliases[alias]
	if !ok {
		return "", nil, fmt.Errorf("query: unknown alias %q", alias)
	}
	f := m.Field(name)
	if f == nil {
		if a := m.Assoc(name); a != nil && a.Owning && a.Kind.IsToOne() {
			f = m.FieldByColumn(a.JoinColumns[0].Name)
		}
	}
	if f == nil {
		f = m.FieldByColumn(name)
	}
	if f == nil {
		return "", nil, fmt.Errorf("query: %s has no field %q", m.Name, name)
	}
	return alias + "." + f.Column, f, nil
}

// aliasOf returns the alias of the entity a FieldRef belongs to: the root if
// it is that entity, otherwise the only join of it.
func (q *Query) aliasOf(r mapping.FieldRef) (string, error) {
	alias := ""
	for a, m := range q.aliases {
		if m.Name != r.Entity {
			continue
		}
		if a == q.root {
			return a, nil
		}
		if alias != "" {
			return "", fmt.Errorf("query: %s.%s is ambiguous; use \"alias.%s\"", r.Entity, r.Name, r.Name)
		}
		alias = a
	}
	if alias == "" {
		return "", fmt.Errorf("query: entity %s is not part of the query", r.Entity)
	}
	return alias, nil
}

// build renders the query. countOnly selects COUNT instead of entities.
func (q *Query) build(count bool) (*dbal.SelectQuery, error) { return q.buildMode(count, false) }

// buildMode renders the query; refs selects only identifier and
// discriminator (for polymorphic queries).
func (q *Query) buildMode(count, refs bool) (*dbal.SelectQuery, error) {
	if q.err != nil {
		return nil, q.err
	}
	em, meta := q.em, q.meta
	wrap := q.hasToManyJoin() && meta.SingleID() != nil
	inner := em.db.NewSelect()
	em.fromEntity(inner, meta, q.root)
	for _, j := range q.joins {
		if err := q.appendJoin(inner, j); err != nil {
			return nil, err
		}
	}
	if len(q.where) > 0 {
		e, err := And(q.where...).build(q)
		if err != nil {
			return nil, err
		}
		inner.WhereExpr(e)
	}
	for _, c := range append(em.filterCondsExcept(meta, q.root, q.without), classConds(meta, q.root, !meta.IsPolymorphic())...) {
		inner.WhereExpr(c)
	}
	idCol := ""
	if id := meta.SingleID(); id != nil {
		idCol = q.root + "." + id.Column
	}
	if len(q.columns) > 0 && !count {
		return q.projection(inner)
	}
	if count {
		switch {
		case wrap:
			inner.ColumnExpr("COUNT(DISTINCT ?)", dbal.Ident(idCol))
		case q.hasToManyJoin():
			return nil, fmt.Errorf("query: counting with collection joins needs a single column identifier")
		default:
			inner.ColumnExpr("COUNT(*)")
		}
		return inner, nil
	}
	var out *dbal.SelectQuery
	switch {
	case wrap && refs:
		inner.Column(idCol)
		out = em.db.NewSelect().Column(idCol, q.root+"."+meta.DiscriminatorColumn)
		em.fromEntity(out, meta, q.root)
		out.Where("? IN ?", dbal.Ident(idCol), inner)
	case wrap:
		inner.Column(idCol)
		out = em.selectEntity(meta, q.root).Where("? IN ?", dbal.Ident(idCol), inner)
	case refs:
		out = inner.Column(idCol, q.root+"."+meta.DiscriminatorColumn)
	default:
		out = inner.ColumnExprs(entityColumns(meta, q.root)...)
		if q.hasToManyJoin() {
			out.Distinct()
		}
	}
	for _, o := range q.order {
		if r, ok := o.field.(rawCond); ok {
			expr, err := r.build(q)
			if err != nil {
				return nil, err
			}
			dir := o.dir
			if dir != dbal.Desc {
				dir = dbal.Asc
			}
			out.OrderExpr("? "+string(dir), expr)
			continue
		}
		col, _, err := q.resolve(o.field)
		if err != nil {
			return nil, err
		}
		if wrap && !strings.HasPrefix(col, q.root+".") {
			return nil, fmt.Errorf("query: ordering by %v is not possible together with collection joins", o.field)
		}
		out.OrderBy(col, o.dir)
	}
	out.Limit(q.limit).Offset(q.offset)
	if q.lock != dbal.LockNone {
		out.Lock(q.lock)
	}
	return out, nil
}

// projection finishes inner as a selection of Columns.
func (q *Query) projection(out *dbal.SelectQuery) (*dbal.SelectQuery, error) {
	if q.distinct {
		out.Distinct()
	}
	for _, c := range q.columns {
		e, err := c.build(q)
		if err != nil {
			return nil, err
		}
		out.ColumnExprs(e)
	}
	for _, g := range q.groupBy {
		e, err := q.template(g)
		if err != nil {
			return nil, err
		}
		out.GroupByExpr("?", e)
	}
	for _, o := range q.order {
		var e dbal.Expr
		var err error
		if r, ok := o.field.(rawCond); ok {
			e, err = r.build(q)
		} else {
			var col string
			col, _, err = q.resolve(o.field)
			e = dbal.Ident(col)
		}
		if err != nil {
			return nil, err
		}
		dir := o.dir
		if dir != dbal.Desc {
			dir = dbal.Asc
		}
		out.OrderExpr("? "+string(dir), e)
	}
	return out.Limit(q.limit).Offset(q.offset), nil
}

func (q *Query) appendJoin(s *dbal.SelectQuery, j queryJoin) error {
	if j.sub != nil {
		return q.appendJoinSub(s, j)
	}
	if j.on != nil {
		return q.appendJoinOn(s, j)
	}
	a := j.assoc
	join := s.InnerJoin
	if j.left {
		join = s.LeftJoin
	}
	col := func(alias, c string) dbal.Ident { return dbal.Ident(alias + "." + c) }
	// Filters of the joined entity become part of the ON clause, so LEFT
	// JOINs keep their semantics.
	on := "? = ?"
	var extra []any
	for _, c := range append(q.em.filterCondsExcept(a.Target, j.alias, q.without), classConds(a.Target, j.alias, false)...) {
		on += " AND ?"
		extra = append(extra, c)
	}
	kind := "INNER JOIN"
	if j.left {
		kind = "LEFT JOIN"
	}
	target := func(left, right dbal.Ident) {
		args := append([]any{q.em.source(a.Target), dbal.Ident(j.alias), left, right}, extra...)
		s.Join(kind+" ? AS ? ON "+on, args...)
	}
	switch {
	case a.Kind.IsToOne() && a.Owning:
		jc := a.JoinColumns[0]
		target(col(j.alias, jc.Referenced), col(j.from, jc.Name))
	case a.Kind.IsToOne(): // inverse oneToOne
		jc := a.Inverse().JoinColumns[0]
		target(col(j.alias, jc.Name), col(j.from, jc.Referenced))
	case a.Kind == mapping.OneToMany:
		spec := toManySpec(a)
		target(col(j.alias, spec.ownerCol), col(j.from, spec.ownerRef.Column))
	default:
		spec := toManySpec(a)
		jt := j.alias + "_jt"
		join(spec.table, jt, "? = ?", col(jt, spec.ownerCol), col(j.from, spec.ownerRef.Column))
		target(col(j.alias, spec.targetRef.Column), col(jt, spec.targetCol))
	}
	return nil
}

// appendJoinSub joins a sub query.
func (q *Query) appendJoinSub(s *dbal.SelectQuery, j queryJoin) error {
	if j.lateral && q.em.db.Dialect().Name() != dialect.Postgres {
		return fmt.Errorf("query: JOIN LATERAL is not supported by %s", q.em.db.Dialect().Name())
	}
	kind := "INNER JOIN "
	if j.left {
		kind = "LEFT JOIN "
	}
	if j.lateral {
		s.Join(kind+"LATERAL ? AS ? ON true", j.sub, dbal.Ident(j.alias))
		return nil
	}
	on, err := j.on.build(q)
	if err != nil {
		return err
	}
	s.Join(kind+"? AS ? ON ?", j.sub, dbal.Ident(j.alias), on)
	return nil
}

// appendJoinOn joins an entity on a condition; its filters join the ON
// clause, so LEFT JOINs keep their semantics.
func (q *Query) appendJoinOn(s *dbal.SelectQuery, j queryJoin) error {
	on, err := j.on.build(q)
	if err != nil {
		return err
	}
	tmpl, args := "? AS ? ON ?", []any{q.em.source(j.target), dbal.Ident(j.alias), on}
	for _, c := range append(q.em.filterCondsExcept(j.target, j.alias, q.without), classConds(j.target, j.alias, false)...) {
		tmpl += " AND ?"
		args = append(args, c)
	}
	kind := "INNER JOIN "
	if j.left {
		kind = "LEFT JOIN "
	}
	s.Join(kind+tmpl, args...)
	return nil
}

// SQL renders the query for debugging.
func (q *Query) SQL() (string, []any, error) {
	s, err := q.build(false)
	if err != nil {
		return "", nil, err
	}
	return s.Build()
}

// All returns every matching entity.
func (q *Query) All(ctx context.Context) ([]any, error) {
	if q.em.closed {
		return nil, ErrClosed
	}
	var res []any
	if q.meta != nil && q.meta.IsPolymorphic() {
		s, err := q.buildMode(false, true)
		if err != nil {
			return nil, err
		}
		refs, err := q.em.scanRefs(ctx, s, q.meta.SingleID())
		if err != nil {
			return nil, err
		}
		if res, err = q.em.loadRefs(ctx, q.meta, refs); err != nil {
			return nil, err
		}
		if len(q.with) == 0 || len(res) == 0 {
			return res, nil
		}
		return res, q.em.Preload(ctx, res, q.with...)
	}
	s, err := q.build(false)
	if err != nil {
		return nil, err
	}
	with := q.with
	if joins := q.fetchJoins(); len(joins) > 0 {
		with = make([]string, 0, len(q.with))
		for _, w := range q.with {
			if !slices.ContainsFunc(joins, func(a *mapping.Association) bool { return a.Name == w }) {
				with = append(with, w)
			}
		}
		q.addFetchJoins(s, joins)
		res, err = q.em.loadJoined(ctx, q.meta, s, joins)
	} else {
		res, err = q.em.loadQuery(ctx, q.meta, s, nil)
	}
	if err != nil || len(with) == 0 || len(res) == 0 {
		return res, err
	}
	return res, q.em.Preload(ctx, res, with...)
}

// fetchJoins returns the associations in q.with that All loads with a LEFT
// JOIN in the same statement instead of a second query: owning to-one
// associations to a single-table entity without active filters, keyed by
// its identifier. Queries that lock rows or join collections preload them
// as usual.
func (q *Query) fetchJoins() []*mapping.Association {
	if len(q.with) == 0 || q.lock != dbal.LockNone || q.hasToManyJoin() || q.meta.SingleID() == nil {
		return nil
	}
	var out []*mapping.Association
	for _, w := range q.with {
		a := q.meta.Assoc(w)
		if a == nil || !a.Kind.IsToOne() || !a.Owning || len(a.JoinColumns) != 1 || slices.Contains(out, a) ||
			(a.Container != mapping.ContainerPointer && a.Container != mapping.ContainerRef) {
			continue
		}
		t := a.Target
		if t.IsPolymorphic() || t.Inheritance != "" || len(t.Tables()) != 1 || t.SingleID() == nil ||
			t.SingleID().Column != a.JoinColumns[0].Referenced || len(q.em.filterCondsExcept(t, "", q.without)) > 0 {
			continue
		}
		out = append(out, a)
	}
	return out
}

// fetchAlias is the alias of the i-th fetch join.
func fetchAlias(i int) string { return RootAlias + "_f" + strconv.Itoa(i) }

// addFetchJoins adds the LEFT JOINs and target columns of joins to s.
func (q *Query) addFetchJoins(s *dbal.SelectQuery, joins []*mapping.Association) {
	for i, a := range joins {
		alias := fetchAlias(i)
		jc := a.JoinColumns[0]
		s.Join("LEFT JOIN ? AS ? ON ? = ?", q.em.source(a.Target), dbal.Ident(alias),
			dbal.Ident(alias+"."+jc.Referenced), dbal.Ident(q.root+"."+jc.Name))
		s.ColumnExprs(entityColumns(a.Target, alias)...)
	}
}

// One returns the first matching entity or ErrNotFound.
func (q *Query) One(ctx context.Context) (any, error) {
	c := q.Clone()
	c.limit = 1
	res, err := c.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, ErrNotFound
	}
	return res[0], nil
}

// Rows runs a projection (Columns) and returns its rows.
func (q *Query) Rows(ctx context.Context) (*sql.Rows, error) {
	if q.em.closed {
		return nil, ErrClosed
	}
	if len(q.columns) == 0 {
		return nil, fmt.Errorf("query: Rows needs Columns")
	}
	s, err := q.build(false)
	if err != nil {
		return nil, err
	}
	return s.Rows(ctx)
}

// Count returns the number of matching entities, ignoring limit and offset.
func (q *Query) Count(ctx context.Context) (int, error) {
	s, err := q.build(true)
	if err != nil {
		return 0, err
	}
	var n int
	return n, s.Scan(ctx, &n)
}

// Exists reports whether at least one entity matches.
func (q *Query) Exists(ctx context.Context) (bool, error) {
	n, err := q.Count(ctx)
	return n > 0, err
}

// Iterate streams the results row by row. Entities stay in the identity
// map; call EntityManager.Clear periodically for very large results.
// Preloading (With) is not applied.
func (q *Query) Iterate(ctx context.Context) iter.Seq2[any, error] {
	return func(yield func(any, error) bool) {
		if q.meta != nil && q.meta.IsPolymorphic() {
			res, err := q.All(ctx)
			if err != nil {
				yield(nil, err)
				return
			}
			for _, v := range res {
				if !yield(v, nil) {
					return
				}
			}
			return
		}
		s, err := q.build(false)
		if err != nil {
			yield(nil, err)
			return
		}
		rows, err := s.Rows(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		defer rows.Close()
		h := q.em.newHydrator(q.meta)
		for rows.Next() {
			v, err := h.row(ctx, rows, nil)
			if !yield(v, err) || err != nil {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(nil, err)
		}
	}
}
