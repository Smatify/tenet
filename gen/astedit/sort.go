package astedit

import (
	"go/ast"
	"go/token"
)

func astSortImports(fset *token.FileSet, f *ast.File) { ast.SortImports(fset, f) }
