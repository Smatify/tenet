package smorm

import (
	"context"
	"iter"
	"reflect"
	"sort"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/orm"
)

// Cond is a query condition.
type Cond = orm.Cond

// Conditions.
var (
	Eq        = orm.Eq
	Neq       = orm.Neq
	Gt        = orm.Gt
	Gte       = orm.Gte
	Lt        = orm.Lt
	Lte       = orm.Lte
	Like      = orm.Like
	IsNull    = orm.IsNull
	IsNotNull = orm.IsNotNull
	In        = orm.In
	NotIn     = orm.NotIn
	And       = orm.And
	Or        = orm.Or
	Not       = orm.Not
	Raw       = orm.Raw
)

// Sort directions.
const (
	Asc  = dbal.Asc
	Desc = dbal.Desc
)

// Query is a typed entity query. The root entity has the alias "t0";
// joined associations get the alias passed to Join.
type Query[T any] struct{ q *orm.Query }

// NewQuery starts a query for T.
func NewQuery[T any](em *EntityManager) *Query[T] {
	return &Query[T]{q: em.NewQuery(reflect.TypeFor[T]())}
}

// Join adds an INNER JOIN over an association ("Author" or "a.Posts").
func (q *Query[T]) Join(path, alias string) *Query[T] { q.q.Join(path, alias); return q }

// LeftJoin adds a LEFT JOIN over an association.
func (q *Query[T]) LeftJoin(path, alias string) *Query[T] { q.q.LeftJoin(path, alias); return q }

// Where adds conditions joined with AND.
func (q *Query[T]) Where(conds ...Cond) *Query[T] { q.q.Where(conds...); return q }

// OrderBy sorts by a field or by an expression given as Raw.
func (q *Query[T]) OrderBy(field any, dir dbal.Order) *Query[T] { q.q.OrderBy(field, dir); return q }

// Limit sets the maximum number of results.
func (q *Query[T]) Limit(n int) *Query[T] { q.q.Limit(n); return q }

// Offset skips results.
func (q *Query[T]) Offset(n int) *Query[T] { q.q.Offset(n); return q }

// WithoutFilter disables the named filters for this query only.
func (q *Query[T]) WithoutFilter(names ...string) *Query[T] { q.q.WithoutFilter(names...); return q }

// WithDeleted includes soft-deleted rows in this query.
func (q *Query[T]) WithDeleted() *Query[T] { q.q.WithDeleted(); return q }

// With preloads association paths of the results.
func (q *Query[T]) With(paths ...string) *Query[T] { q.q.With(paths...); return q }

// ForUpdate locks the selected rows.
func (q *Query[T]) ForUpdate() *Query[T] { q.q.ForUpdate(); return q }

// Clone returns an independent copy.
func (q *Query[T]) Clone() *Query[T] { return &Query[T]{q: q.q.Clone()} }

// SQL renders the query for debugging.
func (q *Query[T]) SQL() (string, []any, error) { return q.q.SQL() }

// All returns every matching entity.
func (q *Query[T]) All(ctx context.Context) ([]*T, error) {
	res, err := q.q.All(ctx)
	return typed[T](res), err
}

// One returns the first matching entity or ErrNotFound.
func (q *Query[T]) One(ctx context.Context) (*T, error) {
	v, err := q.q.One(ctx)
	if err != nil {
		return nil, err
	}
	return v.(*T), nil
}

// Count returns the number of matching entities.
func (q *Query[T]) Count(ctx context.Context) (int, error) { return q.q.Count(ctx) }

// Exists reports whether an entity matches.
func (q *Query[T]) Exists(ctx context.Context) (bool, error) { return q.q.Exists(ctx) }

// Iterate streams the results.
func (q *Query[T]) Iterate(ctx context.Context) iter.Seq2[*T, error] {
	return func(yield func(*T, error) bool) {
		for v, err := range q.q.Iterate(ctx) {
			var p *T
			if v != nil {
				p = v.(*T)
			}
			if !yield(p, err) {
				return
			}
		}
	}
}

func typed[T any](in []any) []*T {
	out := make([]*T, len(in))
	for i, v := range in {
		out[i] = v.(*T)
	}
	return out
}

// Page is one page of results.
type Page[T any] struct {
	Items      []*T
	Page       int // 1-based
	PerPage    int
	Total      int
	TotalPages int
}

// HasNext reports whether a following page exists.
func (p Page[T]) HasNext() bool { return p.Page < p.TotalPages }

// HasPrev reports whether a previous page exists.
func (p Page[T]) HasPrev() bool { return p.Page > 1 }

// Paginate returns page (1-based) of q with perPage items. Collection joins
// are handled without duplicates.
func Paginate[T any](ctx context.Context, q *Query[T], page, perPage int) (Page[T], error) {
	page, perPage = max(page, 1), max(perPage, 1)
	total, err := q.Count(ctx)
	if err != nil {
		return Page[T]{}, err
	}
	items, err := q.Clone().Limit(perPage).Offset((page - 1) * perPage).All(ctx)
	if err != nil {
		return Page[T]{}, err
	}
	return Page[T]{Items: items, Page: page, PerPage: perPage, Total: total, TotalPages: (total + perPage - 1) / perPage}, nil
}

// Criteria maps field names to values for FindBy: a slice means IN, nil
// means IS NULL, an entity pointer compares the association.
type Criteria map[string]any

// FindOption tunes FindBy.
type FindOption func(*orm.Query)

// OrderBy sorts FindBy results.
func OrderBy(field string, dir dbal.Order) FindOption {
	return func(q *orm.Query) { q.OrderBy(field, dir) }
}

// Limit limits FindBy results.
func Limit(n int) FindOption { return func(q *orm.Query) { q.Limit(n) } }

// Offset skips FindBy results.
func Offset(n int) FindOption { return func(q *orm.Query) { q.Offset(n) } }

// With preloads associations of FindBy results.
func With(paths ...string) FindOption { return func(q *orm.Query) { q.With(paths...) } }

// Repository gives typed access to the entities of T, like Doctrine's
// EntityRepository. Embed it in custom repositories:
//
//	type PostRepository struct{ *smorm.Repository[Post] }
type Repository[T any] struct{ em *EntityManager }

// RepositoryFor returns the repository of T.
func RepositoryFor[T any](em *EntityManager) *Repository[T] { return &Repository[T]{em: em} }

// EntityManager returns the entity manager.
func (r *Repository[T]) EntityManager() *EntityManager { return r.em }

// Find loads an entity by identifier (ErrNotFound if missing).
func (r *Repository[T]) Find(ctx context.Context, id ...any) (*T, error) {
	return Find[T](ctx, r.em, id...)
}

// FindAll returns every entity.
func (r *Repository[T]) FindAll(ctx context.Context, opts ...FindOption) ([]*T, error) {
	return r.FindBy(ctx, nil, opts...)
}

// FindBy returns the entities matching criteria.
func (r *Repository[T]) FindBy(ctx context.Context, criteria Criteria, opts ...FindOption) ([]*T, error) {
	return r.criteriaQuery(criteria, opts).All(ctx)
}

// FindOneBy returns the first entity matching criteria or ErrNotFound.
func (r *Repository[T]) FindOneBy(ctx context.Context, criteria Criteria, opts ...FindOption) (*T, error) {
	return r.criteriaQuery(criteria, opts).One(ctx)
}

// Count returns the number of entities matching criteria.
func (r *Repository[T]) Count(ctx context.Context, criteria Criteria) (int, error) {
	return r.criteriaQuery(criteria, nil).Count(ctx)
}

// CreateQuery starts a query for custom repository methods.
func (r *Repository[T]) CreateQuery() *Query[T] { return NewQuery[T](r.em) }

func (r *Repository[T]) criteriaQuery(criteria Criteria, opts []FindOption) *Query[T] {
	q := NewQuery[T](r.em)
	keys := make([]string, 0, len(criteria))
	for k := range criteria {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := criteria[k]
		rv := reflect.ValueOf(v)
		if v != nil && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Type().Elem().Kind() != reflect.Uint8 {
			q.Where(In(k, v))
			continue
		}
		q.Where(Eq(k, v))
	}
	for _, o := range opts {
		o(q.q)
	}
	return q
}

// ResultSetMapping maps native result columns to entity columns.
type ResultSetMapping = orm.ResultSetMapping

// Native runs a hand written SQL query (? placeholders) and hydrates T from
// its rows; see orm.EntityManager.NativeQuery. rsm may be nil.
func Native[T any](ctx context.Context, em *EntityManager, rsm *ResultSetMapping, sql string, args ...any) ([]*T, error) {
	res, err := em.NativeQuery(ctx, reflect.TypeFor[T](), rsm, sql, args...)
	return typed[T](res), err
}
