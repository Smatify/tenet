package vet

import (
	"go/ast"
	"go/constant"
	"go/types"
	"reflect"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"

	"github.com/smatify/smorm/internal/brand"
)

// condFuncs take a field name as their first argument.
var condFuncs = map[string]bool{"Eq": true, "Neq": true, "Gt": true, "Gte": true, "Lt": true, "Lte": true,
	"Like": true, "IsNull": true, "IsNotNull": true, "In": true, "NotIn": true}

var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\}`)

// smormObj returns the name of the function or variable called, when it is
// declared in the root package or in orm.
func smormObj(pass *analysis.Pass, call *ast.CallExpr) string {
	obj := typeutilCallee(pass, call)
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	if p := obj.Pkg().Path(); p != rootPath && p != rootPath+"/orm" {
		return ""
	}
	return obj.Name()
}

// queryEntity returns the entity T of a method call on Query[T] or
// Repository[T], and the method name.
func queryEntity(pass *analysis.Pass, call *ast.CallExpr) (*types.Named, *types.Struct, string) {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil, nil, ""
	}
	s, ok := pass.TypesInfo.Selections[sel]
	if !ok || s.Kind() != types.MethodVal {
		return nil, nil, ""
	}
	recv := s.Recv()
	if p, ok := recv.(*types.Pointer); ok {
		recv = p.Elem()
	}
	n, ok := types.Unalias(recv).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != rootPath || n.TypeArgs().Len() != 1 {
		return nil, nil, ""
	}
	if name := n.Obj().Name(); name != "Query" && name != "Repository" {
		return nil, nil, ""
	}
	named, st := entityStruct(n.TypeArgs().At(0))
	return named, st, sel.Sel.Name
}

// checkFieldNames reports string field names in queries that T does not
// have. Names with an alias ("a.Name") are not checked.
func checkFieldNames(pass *analysis.Pass, call *ast.CallExpr) {
	named, st, method := queryEntity(pass, call)
	if st == nil {
		return
	}
	c := fieldChecker{pass: pass, st: st, entity: named.Obj().Name()}
	switch method {
	case "Where":
		for _, a := range call.Args {
			c.cond(a)
		}
	case "OrderBy":
		if len(call.Args) > 0 {
			c.fieldArg(call.Args[0])
		}
	case "Join", "LeftJoin":
		if len(call.Args) > 0 {
			c.fieldArg(call.Args[0]) // "alias.Assoc" is not checked
		}
	case "With":
		for _, a := range call.Args {
			c.path(a)
		}
	case "FindAll", "FindBy", "FindOneBy", "Count":
		for _, a := range call.Args {
			c.criteria(a)
			c.option(a)
		}
	}
}

type fieldChecker struct {
	pass   *analysis.Pass
	st     *types.Struct
	entity string
}

func (c fieldChecker) literal(e ast.Expr) (string, bool) {
	tv, ok := c.pass.TypesInfo.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// cond checks a condition built with Eq & co., Raw, And, Or or Not.
func (c fieldChecker) cond(e ast.Expr) {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok {
		return
	}
	switch name := smormObj(c.pass, call); {
	case condFuncs[name] && len(call.Args) > 0:
		c.fieldArg(call.Args[0])
	case name == "Raw" && len(call.Args) > 0:
		c.raw(call.Args[0])
	case name == "And" || name == "Or" || name == "Not":
		for _, a := range call.Args {
			c.cond(a)
		}
	}
}

// fieldArg checks a field name given as string literal or as Raw.
func (c fieldChecker) fieldArg(e ast.Expr) {
	if s, ok := c.literal(e); ok {
		c.check(e, s)
		return
	}
	if call, ok := ast.Unparen(e).(*ast.CallExpr); ok && smormObj(c.pass, call) == "Raw" && len(call.Args) > 0 {
		c.raw(call.Args[0])
	}
}

func (c fieldChecker) raw(e ast.Expr) {
	s, ok := c.literal(e)
	if !ok {
		return
	}
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		c.check(e, m[1])
	}
}

// path checks the first segment of a preload path ("Author.Posts").
func (c fieldChecker) path(e ast.Expr) {
	if s, ok := c.literal(e); ok {
		first, _, _ := strings.Cut(s, ".")
		c.check(e, first)
	}
}

func (c fieldChecker) option(e ast.Expr) {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok {
		return
	}
	switch smormObj(c.pass, call) {
	case "OrderBy":
		if len(call.Args) > 0 {
			c.fieldArg(call.Args[0])
		}
	case "With":
		for _, a := range call.Args {
			c.path(a)
		}
	}
}

func (c fieldChecker) criteria(e ast.Expr) {
	lit, ok := ast.Unparen(e).(*ast.CompositeLit)
	if !ok {
		return
	}
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			c.fieldArg(kv.Key)
		}
	}
}

// check reports name unless it is a field of the entity. A dotted name is
// only checked when its first part is a field (an embeddable); otherwise
// the first part is a join alias.
func (c fieldChecker) check(e ast.Expr, name string) {
	if first, _, ok := strings.Cut(name, "."); ok {
		if !hasField(c.st, first) {
			return
		}
		name = first
	}
	if !hasField(c.st, name) {
		c.pass.Reportf(e.Pos(), "%s has no field %q", c.entity, name)
	}
}

// hasField reports whether st (or an embedded struct) has a field with the
// Go name, the column or the join column name (also derived: "created_at",
// "author_id").
func hasField(st *types.Struct, name string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	for i := range st.NumFields() {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get(brand.TagKey)
		if tag == "-" {
			continue
		}
		if f.Name() == name || norm(f.Name()) == norm(name) || norm(f.Name())+"id" == norm(name) {
			return true
		}
		for _, opt := range strings.Split(tag, ",") {
			if k, v, _ := strings.Cut(strings.TrimSpace(opt), ":"); (k == "column" || k == "joinColumn") && strings.EqualFold(v, name) {
				return true
			}
		}
		if f.Embedded() {
			if inner, ok := f.Type().Underlying().(*types.Struct); ok && hasField(inner, name) {
				return true
			}
		}
	}
	return false
}
