// Package vet provides a go/analysis analyzer that finds direct accesses to
// unexported entity fields that are not preceded by smorm.Touch (such code
// reads zero values from uninitialized references, lazy loading proxies),
// and field names in queries that the entity does not have.
package vet

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"

	"github.com/smatify/smorm/internal/brand"
)

const (
	rootPath   = "github.com/smatify/smorm"
	headerPkg  = rootPath + "/internal/entity"
	headerName = "Header"
)

// IgnoreDirective in a function's doc comment disables the check for it.
var IgnoreDirective = brand.Name + "vet:ignore"

// Analyzer reports field accesses without Touch and unknown field names.
var Analyzer = &analysis.Analyzer{
	Name:     brand.Name + "vet",
	Doc:      "report entity field accesses that are not preceded by " + brand.Name + ".Touch (lazy loading would read zero values) and unknown field names in queries",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func isHeader(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == headerName && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == headerPkg
}

// entityStruct returns the struct of an entity type (T or *T), or nil.
func entityStruct(t types.Type) (*types.Named, *types.Struct) {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return nil, nil
	}
	st, ok := n.Underlying().(*types.Struct)
	if !ok {
		return nil, nil
	}
	for i := range st.NumFields() {
		if f := st.Field(i); f.Embedded() && isHeader(f.Type()) {
			return n, st
		}
	}
	return nil, nil
}

// lazyField reports whether f is an unexported mapped field of st that
// needs Touch: not ignored ("-"), not the identifier.
func lazyField(st *types.Struct, f *types.Var) bool {
	if f.Exported() || f.Embedded() {
		return false
	}
	for i := range st.NumFields() {
		if st.Field(i) != f {
			continue
		}
		tag := reflect.StructTag(st.Tag(i)).Get(brand.TagKey)
		if tag == "-" || strings.EqualFold(f.Name(), "id") {
			return false
		}
		for _, opt := range strings.Split(tag, ",") {
			if k, _, _ := strings.Cut(strings.TrimSpace(opt), ":"); k == "id" || k == "pk" {
				return false
			}
		}
		return true
	}
	return false // promoted from an embedded struct
}

func isTouch(pass *analysis.Pass, call *ast.CallExpr) (types.Object, bool) {
	fn, ok := typeutilCallee(pass, call).(*types.Func)
	if !ok || fn.Name() != "Touch" || fn.Pkg() == nil || len(call.Args) != 1 {
		return nil, false
	}
	if p := fn.Pkg().Path(); p != rootPath && p != headerPkg {
		return nil, false
	}
	id, ok := ast.Unparen(call.Args[0]).(*ast.Ident)
	if !ok {
		return nil, false
	}
	return pass.TypesInfo.Uses[id], true
}

func typeutilCallee(pass *analysis.Pass, call *ast.CallExpr) types.Object {
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		return pass.TypesInfo.Uses[f]
	case *ast.SelectorExpr:
		return pass.TypesInfo.Uses[f.Sel]
	}
	return nil
}

func generated(f *ast.File) bool { return ast.IsGenerated(f) }

func run(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	files, gen := map[*token.File]bool{}, map[*token.File]bool{}
	for _, f := range pass.Files {
		tf := pass.Fset.File(f.Pos())
		gen[tf] = generated(f)
		if generated(f) || strings.HasSuffix(tf.Name(), "_test.go") {
			files[tf] = true
		}
	}
	ins.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		if !gen[pass.Fset.File(n.Pos())] {
			checkFieldNames(pass, n.(*ast.CallExpr))
		}
	})
	ins.Preorder([]ast.Node{(*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}, func(n ast.Node) {
		if files[pass.Fset.File(n.Pos())] {
			return
		}
		var body *ast.BlockStmt
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Doc != nil && strings.Contains(fn.Doc.Text(), IgnoreDirective) {
				return
			}
			body = fn.Body
		case *ast.FuncLit:
			return // checked as part of the enclosing function
		}
		if body == nil {
			return
		}
		checkBody(pass, body)
	})
	return nil, nil
}

func checkBody(pass *analysis.Pass, body *ast.BlockStmt) {
	touched := map[types.Object]token.Pos{}
	reported := map[types.Object]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if obj, ok := isTouch(pass, x); ok && obj != nil {
				if _, seen := touched[obj]; !seen {
					touched[obj] = x.Pos()
				}
			}
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			obj := pass.TypesInfo.Uses[id]
			if obj == nil || reported[obj] {
				return true
			}
			named, st := entityStruct(obj.Type())
			if named == nil {
				return true
			}
			sel, ok := pass.TypesInfo.Selections[x]
			if !ok || sel.Kind() != types.FieldVal || len(sel.Index()) != 1 {
				return true
			}
			field, _ := sel.Obj().(*types.Var)
			if field == nil || !lazyField(st, field) {
				return true
			}
			if pos, ok := touched[obj]; ok && pos < x.Pos() {
				return true
			}
			reported[obj] = true
			pass.Reportf(x.Pos(), "%s.%s is accessed without %s.Touch(%s); an uninitialized reference would return the zero value",
				named.Obj().Name(), field.Name(), brand.Name, id.Name)
		}
		return true
	})
}
