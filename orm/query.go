package orm

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
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
}

type queryJoin struct {
	from, path, alias string
	left              bool
	assoc             *mapping.Association
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
	q.aliases[RootAlias] = meta
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
		from, name, path = RootAlias, r, r
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
		if !j.assoc.Kind.IsToOne() {
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
		alias, name = RootAlias, r
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
		if a == RootAlias {
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
	em.fromEntity(inner, meta, RootAlias)
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
	for _, c := range append(em.filterCondsExcept(meta, RootAlias, q.without), classConds(meta, RootAlias, !meta.IsPolymorphic())...) {
		inner.WhereExpr(c)
	}
	idCol := ""
	if id := meta.SingleID(); id != nil {
		idCol = RootAlias + "." + id.Column
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
		out = em.db.NewSelect().Column(idCol, RootAlias+"."+meta.DiscriminatorColumn)
		em.fromEntity(out, meta, RootAlias)
		out.Where("? IN ?", dbal.Ident(idCol), inner)
	case wrap:
		inner.Column(idCol)
		out = em.selectEntity(meta, RootAlias).Where("? IN ?", dbal.Ident(idCol), inner)
	case refs:
		out = inner.Column(idCol, RootAlias+"."+meta.DiscriminatorColumn)
	default:
		out = inner.ColumnExprs(entityColumns(meta, RootAlias)...)
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
		if wrap && !strings.HasPrefix(col, RootAlias+".") {
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

func (q *Query) appendJoin(s *dbal.SelectQuery, j queryJoin) error {
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
			dbal.Ident(alias+"."+jc.Referenced), dbal.Ident(RootAlias+"."+jc.Name))
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
