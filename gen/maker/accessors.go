package maker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/smatify/smorm/gen/astedit"
	"github.com/smatify/smorm/internal/brand"
	"github.com/smatify/smorm/mapping"
)

// mappedField is an unexported mapped field of an entity struct (not the
// identifier or the version, which smorm sets itself).
type mappedField struct {
	entity, name, typ string
	tag               mapping.Tag
	target            string // element type of a Collection
}

// Accessors adds the getters and setters that are missing for the mapped
// unexported fields of the entities in Dir (collection methods for to-many
// associations), e.g. after fields were added by hand. Existing methods are
// kept; with entities given only those are completed. It returns the
// added methods as "Entity.Method".
func (mk *Maker) Accessors(entities ...string) ([]string, error) {
	fields, existing, err := scanEntities(mk.Dir)
	if err != nil {
		return nil, err
	}
	if len(entities) > 0 {
		fields = slices.DeleteFunc(fields, func(f mappedField) bool { return !slices.Contains(entities, f.entity) })
	}
	// Methods present after this run, for the hooks that sync the other
	// side of an association.
	planned := map[string]bool{}
	for k := range existing {
		planned[k] = true
	}
	for _, f := range fields {
		up := upper(f.name)
		if f.target == "" {
			planned[f.entity+"."+up], planned[f.entity+".Set"+up] = true, true
			continue
		}
		one := collectionItem(f.name)
		for _, m := range []string{up, "Add" + one, "Remove" + one, "Set" + up} {
			planned[f.entity+"."+m] = true
		}
	}
	w := &writer{mk: mk, style: StyleDoctrine, files: map[string]*astedit.File{}, existing: existing}
	for _, f := range fields {
		if _, err := w.ensureEntity(f.entity, EntitySpec{Name: f.entity}); err != nil {
			return nil, err
		}
		if f.target == "" {
			if err := w.accessors(f.entity, f.name, f.typ); err != nil {
				return nil, err
			}
			continue
		}
		onAdd, onRemove := syncHooks(f, planned)
		if err := w.collectionMethods(f.entity, f.name, f.target, onAdd, onRemove); err != nil {
			return nil, err
		}
	}
	if len(w.added) == 0 {
		return nil, nil
	}
	if _, err := w.save(); err != nil {
		return nil, err
	}
	return w.added, nil
}

func collectionItem(prop string) string {
	one := upper(singular(prop))
	if one == upper(prop) {
		one += "Item"
	}
	return one
}

// syncHooks returns the statements that update the other side of a
// bidirectional association when an item is added or removed, if the
// methods they call exist.
func syncHooks(f mappedField, planned map[string]bool) (onAdd, onRemove string) {
	r := recv(f.entity)
	other := f.tag.Get("mappedBy")
	if other == "" {
		other = f.tag.Get("inversedBy")
	}
	if other == "" {
		return "", ""
	}
	if f.tag.Has("oneToMany") {
		set := "Set" + upper(other)
		if !planned[f.target+"."+set] || !planned[f.target+"."+upper(other)] {
			return "", ""
		}
		onAdd = "\t\titem." + set + "(" + r + ")\n"
		if !f.tag.Has("orphanRemoval") {
			onRemove = "\t\tif item." + upper(other) + "() == " + r + " {\n\t\t\titem." + set + "(nil)\n\t\t}\n"
		}
		return onAdd, onRemove
	}
	one := collectionItem(other)
	if planned[f.target+".Add"+one] {
		onAdd = "\t\titem.Add" + one + "(" + r + ")\n"
	}
	if planned[f.target+".Remove"+one] {
		onRemove = "\t\titem.Remove" + one + "(" + r + ")\n"
	}
	return onAdd, onRemove
}

// scanEntities returns the unexported mapped fields of the entity structs in
// dir and the methods declared there ("Entity.Method").
func scanEntities(dir string) ([]mappedField, map[string]bool, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	existing := map[string]bool{}
	var fields []mappedField
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return nil, nil, err
		}
		if ast.IsGenerated(file) {
			continue
		}
		root := rootName(file)
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if name := recvName(d); name != "" {
					existing[name+"."+d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					ts, ok := s.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if st, ok := ts.Type.(*ast.StructType); ok && root != "" && isEntity(st, root) {
						fs, err := structFields(ts.Name.Name, st, root)
						if err != nil {
							return nil, nil, err
						}
						fields = append(fields, fs...)
					}
				}
			}
		}
	}
	return fields, existing, nil
}

// rootName is the name under which file imports the root package.
func rootName(file *ast.File) string {
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == importPath() {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return brand.Name
		}
	}
	return ""
}

func recvName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) != 1 {
		return ""
	}
	t := d.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func isEntity(st *ast.StructType, root string) bool {
	for _, f := range st.Fields.List {
		if sel, ok := f.Type.(*ast.SelectorExpr); ok && len(f.Names) == 0 && sel.Sel.Name == "Entity" {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == root {
				return true
			}
		}
	}
	return false
}

func structFields(entity string, st *ast.StructType, root string) ([]mappedField, error) {
	var out []mappedField
	for _, f := range st.Fields.List {
		raw := ""
		if f.Tag != nil {
			raw, _ = strconv.Unquote(f.Tag.Value)
		}
		value := reflect.StructTag(raw).Get(brand.TagKey)
		if value == "-" {
			continue
		}
		tag, err := mapping.ParseTag(value)
		if err != nil {
			return nil, err
		}
		if tag.Has("id") || tag.Has("pk") || tag.Has("embedded") || tag.Has("version") {
			continue
		}
		typ := types.ExprString(f.Type)
		target := ""
		if idx, ok := f.Type.(*ast.IndexExpr); ok {
			if sel, ok := idx.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Collection" && types.ExprString(sel.X) == root {
				target = types.ExprString(idx.Index)
			}
		}
		for _, n := range f.Names {
			if n.IsExported() || strings.EqualFold(n.Name, "id") {
				continue
			}
			out = append(out, mappedField{entity: entity, name: n.Name, typ: typ, tag: tag, target: target})
		}
	}
	return out, nil
}
