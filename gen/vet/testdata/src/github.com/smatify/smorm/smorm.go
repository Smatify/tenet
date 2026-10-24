package smorm

import "github.com/smatify/smorm/internal/entity"

type Entity = entity.Header

func Touch(p any) {}

type Cond interface{}

type Criteria map[string]any

type FindOption func()

type Query[T any] struct{}

func NewQuery[T any]() *Query[T] { return nil }

func (q *Query[T]) Where(conds ...Cond) *Query[T]         { return q }
func (q *Query[T]) OrderBy(field any, dir int) *Query[T]  { return q }
func (q *Query[T]) Join(path any, alias string) *Query[T] { return q }
func (q *Query[T]) With(paths ...any) *Query[T]           { return q }

type Repository[T any] struct{}

func (r *Repository[T]) FindBy(c Criteria, opts ...FindOption) {}

var (
	Eq     = func(field, value any) Cond { return nil }
	IsNull = func(field any) Cond { return nil }
	Raw    = func(sql string, args ...any) Cond { return nil }
	Or     = func(conds ...Cond) Cond { return nil }
)

func OrderBy(field any, dir int) FindOption { return nil }
func With(paths ...any) FindOption          { return nil }
