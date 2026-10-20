package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/mapping"
)

// isA reports whether class is meta or one of its subclasses.
func isA(class, meta *mapping.Entity) bool {
	for c := class; c != nil; c = c.Parent {
		if c == meta {
			return true
		}
	}
	return false
}

// view returns a pointer to the part of e's instance that has type meta
// (the instance itself, or its embedded ancestor struct).
func view(e *entry, meta *mapping.Entity) any {
	if e.meta == meta {
		return e.ptr
	}
	var path []int
	for c := e.meta; c != nil && c != meta; c = c.Parent {
		path = append(path, c.ParentIndex...)
	}
	v := reflect.ValueOf(e.ptr).Elem().FieldByIndex(path)
	// The embedded parent may be an unexported type; address it directly.
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Interface()
}

// Concrete returns the full instance behind ptr, e.g. the *Car behind a
// *Vehicle returned by a polymorphic query. Other values are returned as is.
func Concrete(ptr any) any {
	if e, ok := entryFromPtr(ptr); ok {
		return e.ptr
	}
	return ptr
}

// source returns the table expression of meta: the table, or for subclasses
// with joined inheritance a derived table joining all their tables.
func (em *EntityManager) source(meta *mapping.Entity) dbal.Expr {
	if meta.Inheritance != mapping.Joined || meta.Parent == nil {
		return dbal.Ident(meta.Table)
	}
	sub := em.db.NewSelect()
	for _, f := range meta.Fields {
		sub.ColumnExpr("? AS ?", dbal.Ident(f.Table+"."+f.Column), dbal.Ident(f.Column))
	}
	root := meta.Root.Table
	sub.Column(root + "." + meta.DiscriminatorColumn)
	tables := meta.Tables()
	sub.Table(tables[len(tables)-1])
	for _, t := range tables[:len(tables)-1] {
		for _, id := range meta.ID {
			sub.InnerJoin(t, "", "? = ?", dbal.Ident(t+"."+id.Column), dbal.Ident(tables[len(tables)-1]+"."+id.Column))
		}
	}
	return sub
}

// fromEntity sets the FROM clause of q to meta's source.
func (em *EntityManager) fromEntity(q *dbal.SelectQuery, meta *mapping.Entity, alias string) {
	if alias == "" {
		if meta.Inheritance != mapping.Joined || meta.Parent == nil {
			q.Table(meta.Table)
			return
		}
		alias = meta.Table
	}
	q.TableExpr("? AS ?", em.source(meta), dbal.Ident(alias))
}

// classConds restricts rows to meta (exact) or meta and its subclasses.
func classConds(meta *mapping.Entity, alias string, exact bool) []dbal.Expr {
	if meta.Inheritance == "" {
		return nil
	}
	col := meta.DiscriminatorColumn
	if alias != "" {
		col = alias + "." + col
	}
	if exact {
		return []dbal.Expr{dbal.SQL("? = ?", dbal.Ident(col), meta.DiscriminatorValue)}
	}
	if meta.Parent == nil {
		return nil // every row of the hierarchy
	}
	var vals []any
	for _, c := range meta.Descendants() {
		vals = append(vals, c.DiscriminatorValue)
	}
	return []dbal.Expr{dbal.SQL("? IN ?", dbal.Ident(col), dbal.In(vals))}
}

type classRef struct {
	id    any
	class string
}

// loadPolymorphic loads entities of meta or any subclass: it reads
// identifiers and discriminators first, then loads every class with its own
// columns, and returns pointers of meta's type in query order.
func (em *EntityManager) loadPolymorphic(ctx context.Context, meta *mapping.Entity, where func(*dbal.SelectQuery), limit int) ([]any, error) {
	idf := meta.SingleID()
	if idf == nil {
		return nil, errors.New("orm: polymorphic loading needs a single column identifier")
	}
	q := em.db.NewSelect().Column(idf.Column, meta.DiscriminatorColumn)
	em.fromEntity(q, meta, "")
	where(q)
	for _, c := range append(em.filterConds(meta, ""), classConds(meta, "", false)...) {
		q.WhereExpr(c)
	}
	if limit > 0 {
		q.Limit(limit)
	}
	refs, err := em.scanRefs(ctx, q, idf)
	if err != nil {
		return nil, err
	}
	return em.loadRefs(ctx, meta, refs)
}

func (em *EntityManager) scanRefs(ctx context.Context, q *dbal.SelectQuery, idf *mapping.Field) ([]classRef, error) {
	var refs []classRef
	d := em.db.Dialect().Name()
	err := q.ForEach(ctx, func(rows *sql.Rows) error {
		id := reflect.New(idf.GoType)
		var class string
		if err := rows.Scan(idf.Type.ScanTarget(id.Interface(), d), &class); err != nil {
			return err
		}
		refs = append(refs, classRef{id: id.Elem().Interface(), class: class})
		return nil
	})
	return refs, err
}

// loadRefs loads the classes of refs and returns views of meta in order.
func (em *EntityManager) loadRefs(ctx context.Context, meta *mapping.Entity, refs []classRef) ([]any, error) {
	idf := meta.SingleID()
	byClass := map[string][]any{}
	var order []string
	for _, r := range refs {
		if _, ok := byClass[r.class]; !ok {
			order = append(order, r.class)
		}
		byClass[r.class] = append(byClass[r.class], idArg{f: idf, v: r.id})
	}
	for _, name := range order {
		class := meta.ClassForDiscriminator(name)
		if class == nil || !isA(class, meta) {
			return nil, fmt.Errorf("orm: unknown discriminator value %q for %s", name, meta.Name)
		}
		ids := byClass[name]
		for len(ids) > 0 {
			n := min(len(ids), preloadChunk)
			chunk := ids[:n]
			ids = ids[n:]
			if _, err := em.loadConcrete(ctx, class, func(q *dbal.SelectQuery) {
				q.Where("? IN ?", dbal.Ident(idf.Column), dbal.In(chunk))
			}, 0, nil); err != nil {
				return nil, err
			}
		}
	}
	out := make([]any, 0, len(refs))
	for _, r := range refs {
		if e := em.lookup(meta, idKey([]any{r.id})); e != nil && e.initialized {
			out = append(out, view(e, meta))
		}
	}
	return out, nil
}
