// Package astedit modifies Go source files while keeping their formatting
// and comments (it uses dave/dst). The maker uses it to add fields and
// methods to existing entities.
package astedit

import (
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
)

// File is a Go source file being edited.
type File struct {
	path string
	f    *dst.File
}

// Open parses the file at path.
func Open(path string) (*File, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, src)
}

// Parse parses src (path is used for error messages and Save).
func Parse(path string, src []byte) (*File, error) {
	f, err := decorator.ParseFile(token.NewFileSet(), path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	return &File{path: path, f: f}, nil
}

// Package returns the package name.
func (f *File) Package() string { return f.f.Name.Name }

// Struct returns the struct type named name, or nil.
func (f *File) Struct(name string) *dst.StructType {
	for _, d := range f.f.Decls {
		g, ok := d.(*dst.GenDecl)
		if !ok || g.Tok != token.TYPE {
			continue
		}
		for _, s := range g.Specs {
			if ts, ok := s.(*dst.TypeSpec); ok && ts.Name.Name == name {
				if st, ok := ts.Type.(*dst.StructType); ok {
					return st
				}
			}
		}
	}
	return nil
}

// HasField reports whether the struct has a field (case-insensitive).
func (f *File) HasField(structName, field string) bool {
	st := f.Struct(structName)
	if st == nil {
		return false
	}
	for _, fl := range st.Fields.List {
		for _, n := range fl.Names {
			if equalFold(n.Name, field) {
				return true
			}
		}
	}
	return false
}

// HasMethod reports whether a method with name exists on the type.
func (f *File) HasMethod(typeName, name string) bool {
	for _, d := range f.f.Decls {
		fn, ok := d.(*dst.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != name || len(fn.Recv.List) == 0 {
			continue
		}
		t := fn.Recv.List[0].Type
		if s, ok := t.(*dst.StarExpr); ok {
			t = s.X
		}
		if id, ok := t.(*dst.Ident); ok && id.Name == typeName {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool { return bytes.EqualFold([]byte(a), []byte(b)) }

// AddField appends a field declaration (e.g. "title string `orm:\"length:200\"`")
// to the struct, optionally with a doc comment.
func (f *File) AddField(structName, decl, comment string) error {
	st := f.Struct(structName)
	if st == nil {
		return fmt.Errorf("astedit: struct %s not found in %s", structName, f.path)
	}
	snippet, err := decorator.Parse("package p\ntype _ struct {\n" + decl + "\n}\n")
	if err != nil {
		return fmt.Errorf("astedit: invalid field %q: %w", decl, err)
	}
	field := snippet.Decls[0].(*dst.GenDecl).Specs[0].(*dst.TypeSpec).Type.(*dst.StructType).Fields.List[0]
	field = dst.Clone(field).(*dst.Field)
	field.Decs.Before = dst.NewLine
	if comment != "" {
		field.Decs.Start.Append("// " + comment)
	}
	st.Fields.List = append(st.Fields.List, field)
	return nil
}

// AddDecls appends top level declarations given as Go source.
func (f *File) AddDecls(src string) error {
	snippet, err := decorator.Parse("package p\n" + src)
	if err != nil {
		return fmt.Errorf("astedit: invalid declarations: %w\n%s", err, src)
	}
	for _, d := range snippet.Decls {
		d = dst.Clone(d).(dst.Decl)
		d.Decorations().Before = dst.EmptyLine
		f.f.Decls = append(f.f.Decls, d)
	}
	return nil
}

// AddImport adds an import (with optional name) if it is missing.
func (f *File) AddImport(path, name string) {
	quoted := strconv.Quote(path)
	for _, imp := range f.f.Imports {
		if imp.Path.Value == quoted {
			return
		}
	}
	spec := &dst.ImportSpec{Path: &dst.BasicLit{Kind: token.STRING, Value: quoted}}
	spec.Decs.Before = dst.NewLine
	if name != "" {
		spec.Name = dst.NewIdent(name)
	}
	for _, d := range f.f.Decls {
		if g, ok := d.(*dst.GenDecl); ok && g.Tok == token.IMPORT {
			for _, s := range g.Specs {
				if s.Decorations().After == dst.None {
					s.Decorations().After = dst.NewLine
				}
			}
			// Standard library imports go after the last one of the
			// first group, others at the end.
			at := len(g.Specs)
			if isStd(path) {
				at = 0
				for i, s := range g.Specs {
					if p, _ := strconv.Unquote(s.(*dst.ImportSpec).Path.Value); isStd(p) {
						at = i + 1
					}
				}
				switch {
				case at == 0 && len(g.Specs) > 0:
					spec.Decs.After = dst.EmptyLine
				case at > 0:
					// Take over the separator to the next group.
					prev := g.Specs[at-1].Decorations()
					spec.Decs.After, prev.After = prev.After, dst.NewLine
				}
			}
			g.Specs = slices.Insert(g.Specs, at, dst.Spec(spec))
			if len(g.Specs) > 1 {
				g.Lparen = true
			}
			f.f.Imports = append(f.f.Imports, spec)
			return
		}
	}
	g := &dst.GenDecl{Tok: token.IMPORT, Specs: []dst.Spec{spec}}
	f.f.Decls = append([]dst.Decl{g}, f.f.Decls...)
	f.f.Imports = append(f.f.Imports, spec)
}

// isStd reports whether path belongs to the standard library (no dot in
// its first element).
func isStd(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// Source renders the file (gofmt formatted, imports sorted).
func (f *File) Source() ([]byte, error) {
	var buf bytes.Buffer
	if err := decorator.Fprint(&buf, f.f); err != nil {
		return nil, err
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("astedit: formatting %s: %w", f.path, err)
	}
	return sortImports(out)
}

// Save writes the file back.
func (f *File) Save() error {
	src, err := f.Source()
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, src, 0o644)
}

// sortImports re-parses with go/ast to sort import blocks like gofmt -s.
func sortImports(src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	astSortImports(fset, file)
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
