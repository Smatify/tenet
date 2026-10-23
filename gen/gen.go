// Package gen generates reflection-free accessors and field references for
// the entities of a Go package. Run it through go generate or the CLI; the
// output file is <pkg dir>/entities<GeneratedSuffix>.
package gen

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"golang.org/x/tools/go/packages"

	"github.com/smatify/smorm/internal/brand"
)

const (
	rootPath   = "github.com/smatify/smorm"
	headerPkg  = rootPath + "/internal/entity"
	headerName = "Header"
)

// FileName is the name of the generated file in each package.
var FileName = "entities" + brand.GeneratedSuffix

// Generate writes the generated file for the package in dir. If the package
// has no entities, an existing generated file is removed. It returns the path
// written (or "" when nothing was generated).
func Generate(dir string) (string, error) {
	src, err := Render(dir)
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, FileName)
	if src == nil {
		if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return "", nil
	}
	return out, os.WriteFile(out, src, 0o644)
}

// Render returns the generated source for the package in dir, or nil if it
// declares no entities.
func Render(dir string) ([]byte, error) {
	cfg := &packages.Config{
		Mode:       packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Dir:        dir,
		BuildFlags: []string{"-tags=" + brand.Name + "gen"},
	}
	pkg, err := loadPackage(cfg, dir)
	if err != nil {
		return nil, err
	}
	if len(pkg.Errors) > 0 {
		// The previous generated file may be stale (e.g. it refers to a
		// removed field); retry with an empty one.
		abs, err := filepath.Abs(filepath.Join(dir, FileName))
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err == nil && pkg.Name != "" {
			cfg.Overlay = map[string][]byte{abs: []byte("package " + pkg.Name + "\n")}
			if pkg, err = loadPackage(cfg, dir); err != nil {
				return nil, err
			}
		}
	}
	if len(pkg.Errors) > 0 {
		return nil, fmt.Errorf("gen: %s: %v", pkg.PkgPath, pkg.Errors[0])
	}
	g := &generator{pkg: pkg.Types, imports: map[string]string{}}
	return g.render()
}

func loadPackage(cfg *packages.Config, dir string) (*packages.Package, error) {
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("gen: expected one package in %s, got %d", dir, len(pkgs))
	}
	return pkgs[0], nil
}

type generator struct {
	pkg     *types.Package
	imports map[string]string // path -> name
}

type fieldData struct {
	Path     string // dotted Go path, key for the registry
	Expr     string // Go expression suffix, e.g. ".Timestamps.createdAt"
	Type     string // Go type expression
	Settable bool
}

type entityData struct {
	Name     string
	Header   string
	Fields   []fieldData
	Assocs   []fieldData
	Refs     []refData
	RefsName string
}

type refData struct{ Exported, Name string }

func (g *generator) render() ([]byte, error) {
	var ents []entityData
	scope := g.pkg.Scope()
	names := scope.Names()
	sort.Strings(names)
	for _, name := range names {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok || named.TypeParams().Len() > 0 {
			continue
		}
		st, ok := named.Underlying().(*types.Struct)
		if !ok {
			continue
		}
		header := headerPath(st)
		if header == "" {
			continue
		}
		e := entityData{Name: name, Header: header, RefsName: name + "Fields"}
		g.walk(&e, st, "", "", true, "")
		ents = append(ents, e)
	}
	if len(ents) == 0 {
		return nil, nil
	}
	g.imports["reflect"] = "reflect"
	g.imports[rootPath] = brand.Name
	g.imports[rootPath+"/mapping"] = "mapping"
	var paths []string
	for p := range g.imports {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, map[string]any{
		"Header": brand.GeneratedHeader, "Package": g.pkg.Name(), "Imports": paths, "Names": g.imports,
		"Entities": ents, "Root": brand.Name,
	})
	if err != nil {
		return nil, err
	}
	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gen: formatting generated code: %w\n%s", err, buf.String())
	}
	return src, nil
}

func isHeader(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == headerName && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == headerPkg
}

func hasMethod(t types.Type, name string) bool {
	ms := types.NewMethodSet(t)
	for i := range ms.Len() {
		if ms.At(i).Obj().Name() == name {
			return true
		}
	}
	return false
}

func isEntityType(t types.Type) bool {
	st, ok := t.Underlying().(*types.Struct)
	return ok && headerPath(st) != ""
}

// headerPath returns the selector path to the entity header ("Entity", or
// "vehicle.Entity" for subclasses embedding their parent entity).
func headerPath(st *types.Struct) string {
	for i := range st.NumFields() {
		if f := st.Field(i); f.Embedded() && isHeader(f.Type()) {
			return f.Name()
		}
	}
	for i := range st.NumFields() {
		f := st.Field(i)
		if !f.Embedded() {
			continue
		}
		if sub, ok := f.Type().Underlying().(*types.Struct); ok {
			if p := headerPath(sub); p != "" {
				return f.Name() + "." + p
			}
		}
	}
	return ""
}

var assocKinds = []string{"manyToOne", "oneToMany", "oneToOne", "manyToMany"}

// walk mirrors mapping's field discovery: header skipped, "-" skipped,
// untagged embedded structs flattened.
func (g *generator) walk(e *entityData, st *types.Struct, path, expr string, accessible bool, names string) {
	for i := range st.NumFields() {
		f := st.Field(i)
		if f.Embedded() && isHeader(f.Type()) {
			continue
		}
		tag := reflect.StructTag(st.Tag(i))
		raw, hasTag := tag.Lookup(brand.TagKey)
		if raw == "-" {
			continue
		}
		fpath := f.Name()
		if path != "" {
			fpath = path + "." + f.Name()
		}
		fexpr := expr + "." + f.Name()
		ok := accessible && (f.Exported() || f.Pkg() == g.pkg)
		ft := f.Type()
		if f.Embedded() && (!hasTag || isEntityType(ft)) {
			if s, isStruct := ft.Underlying().(*types.Struct); isStruct && !isTimeOrScanner(ft) {
				g.walk(e, s, fpath, fexpr, ok, names)
				continue
			}
		}
		if hasOption(raw, "embedded") {
			if s, isStruct := ft.Underlying().(*types.Struct); isStruct {
				g.walk(e, s, fpath, fexpr, ok, names+f.Name()+".")
				continue
			}
		}
		isAssoc := false
		for _, k := range assocKinds {
			if hasOption(raw, k) {
				isAssoc = true
			}
		}
		if p, isPtr := ft.(*types.Pointer); isPtr && isEntityType(p.Elem()) {
			isAssoc = true
		}
		if hasMethod(ft, "CollectionElem") || hasMethod(ft, "RefElem") {
			isAssoc = true
		}
		e.Refs = append(e.Refs, refData{Exported: exportName(strings.ReplaceAll(names, ".", "")+f.Name(), e.Refs), Name: names + f.Name()})
		if !ok {
			continue // reflection fallback at runtime
		}
		fd := fieldData{Path: fpath, Expr: fexpr, Type: types.TypeString(ft, g.qualifier), Settable: true}
		if isAssoc {
			e.Assocs = append(e.Assocs, fd)
		} else {
			e.Fields = append(e.Fields, fd)
		}
	}
}

func isTimeOrScanner(t types.Type) bool {
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "time" && n.Obj().Name() == "Time" {
		return true
	}
	return hasMethod(types.NewPointer(t), "Scan") && (hasMethod(t, "Value") || hasMethod(types.NewPointer(t), "Value"))
}

func (g *generator) qualifier(p *types.Package) string {
	if p == g.pkg {
		return ""
	}
	if name, ok := g.imports[p.Path()]; ok {
		return name
	}
	name := p.Name()
	taken := func(n string) bool {
		for _, v := range g.imports {
			if v == n {
				return true
			}
		}
		return n == "reflect" || n == "mapping" || n == brand.Name
	}
	for i := 2; taken(name); i++ {
		name = fmt.Sprintf("%s%d", p.Name(), i)
	}
	g.imports[p.Path()] = name
	return name
}

func exportName(name string, existing []refData) string {
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	out := string(r)
	if strings.EqualFold(out, "id") {
		out = "ID"
	}
	for slices.ContainsFunc(existing, func(x refData) bool { return x.Exported == out }) {
		out += "_"
	}
	return out
}

var tmpl = template.Must(template.New("gen").Parse(`{{.Header}}

package {{.Package}}

import (
{{- range .Imports}}
	{{index $.Names .}} "{{.}}"
{{- end}}
)
{{range $e := .Entities}}
func init() {
	mapping.RegisterGenerated(reflect.TypeFor[{{$e.Name}}](), mapping.GeneratedType{
		New:    func() any { return new({{$e.Name}}) },
		Header: func(p any) *{{$.Root}}.Entity { return &p.(*{{$e.Name}}).{{$e.Header}} },
		Fields: map[string]mapping.GeneratedField{
{{- range $e.Fields}}
			"{{.Path}}": {
				Get: func(p any) any { return p.(*{{$e.Name}}){{.Expr}} },
				Set: func(p any, v any) {
					if v == nil {
						var zero {{.Type}}
						p.(*{{$e.Name}}){{.Expr}} = zero
						return
					}
					p.(*{{$e.Name}}){{.Expr}} = v.({{.Type}})
				},
				Addr: func(p any) any { return &p.(*{{$e.Name}}){{.Expr}} },
			},
{{- end}}
		},
		Assocs: map[string]mapping.GeneratedField{
{{- range $e.Assocs}}
			"{{.Path}}": {
				Get:  func(p any) any { return p.(*{{$e.Name}}){{.Expr}} },
				Addr: func(p any) any { return &p.(*{{$e.Name}}){{.Expr}} },
			},
{{- end}}
		},
	})
}

// {{$e.RefsName}} references the mapped fields of {{$e.Name}} in queries.
var {{$e.RefsName}} = struct {
{{- range $e.Refs}}
	{{.Exported}} mapping.FieldRef
{{- end}}
}{
{{- range $e.Refs}}
	{{.Exported}}: mapping.FieldRef{Entity: "{{$e.Name}}", Name: "{{.Name}}"},
{{- end}}
}
{{end}}`))

// hasOption reports whether the struct tag value contains option key.
func hasOption(raw, key string) bool {
	for _, part := range strings.Split(raw, ",") {
		k, _, _ := strings.Cut(strings.TrimSpace(part), ":")
		if k == key {
			return true
		}
	}
	return false
}
