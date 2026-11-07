package maker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smatify/tenet/gen/astedit"
	"github.com/smatify/tenet/mapping"
)

// ValueKind selects the doc comment of a value struct.
type ValueKind string

// Value struct kinds.
const (
	KindEmbeddable ValueKind = "embeddable"
	KindTrait      ValueKind = "trait"
)

// ApplyValue creates or extends an embeddable or trait struct (no entity
// header, no identifier) with scalar fields and plain getters/setters.
func (mk *Maker) ApplyValue(kind ValueKind, name string, fields []FieldSpec) ([]string, error) {
	if !isIdent(name) || name[0] < 'A' || name[0] > 'Z' {
		return nil, fmt.Errorf("maker: %q is not a valid name (use UpperCamelCase)", name)
	}
	w := &writer{mk: mk, style: StyleDoctrine, files: map[string]*astedit.File{}}
	path := w.path(name)
	if _, err := os.Stat(path); err == nil {
		f, err := astedit.Open(path)
		if err != nil {
			return nil, err
		}
		if f.Struct(name) == nil {
			return nil, fmt.Errorf("maker: %s exists but does not declare %s", path, name)
		}
		w.files[path] = f
	} else {
		if err := os.MkdirAll(mk.Dir, 0o755); err != nil {
			return nil, err
		}
		doc := fmt.Sprintf("%s is an embeddable value object. Use it in entities as a field tagged embedded.", name)
		if kind == KindTrait {
			doc = fmt.Sprintf("%s is a reusable set of fields. Embed it anonymously in entities;\n// its columns are added without prefix.", name)
		}
		src := fmt.Sprintf("package %s\n\n// %s\ntype %s struct {\n}\n", w.packageName(), doc, name)
		f, err := astedit.Parse(path, []byte(src))
		if err != nil {
			return nil, err
		}
		w.files[path] = f
	}
	for _, fs := range fields {
		if fs.Type == "relation" || fs.Type == "embedded" {
			return nil, fmt.Errorf("maker: %s fields must be scalar", kind)
		}
		if err := w.addValueField(name, fs); err != nil {
			return nil, err
		}
	}
	return w.save()
}

func (w *writer) addValueField(name string, f FieldSpec) error {
	if !isIdent(f.Name) {
		return fmt.Errorf("maker: %q is not a valid property name", f.Name)
	}
	if err := w.checkFree(name, f.Name); err != nil {
		return err
	}
	s, err := scalarFor(f)
	if err != nil {
		return err
	}
	file := w.file(name)
	for _, imp := range s.imports {
		file.AddImport(imp, "")
	}
	typ := s.goType
	tags := append([]string(nil), s.tags...)
	if f.Nullable {
		if s.pointer {
			typ = "*" + typ
		} else {
			tags = append(tags, "nullable")
		}
	}
	if f.Unique {
		tags = append(tags, "unique")
	}
	if err := file.AddField(name, lower(f.Name)+" "+typ+tag(tags), ""); err != nil {
		return err
	}
	r, field, up := recv(name), lower(f.Name), upper(f.Name)
	return file.AddDecls(fmt.Sprintf(`// %[3]s returns the %[2]s.
func (%[1]s *%[4]s) %[3]s() %[5]s { return %[1]s.%[2]s }

// Set%[3]s sets the %[2]s.
func (%[1]s *%[4]s) Set%[3]s(%[6]s %[5]s) *%[4]s {
	%[1]s.%[2]s = %[6]s
	return %[1]s
}
`, r, field, up, name, typ, param(r)))
}

// addEmbedded adds an embeddable field to an entity.
func (w *writer) addEmbedded(entity string, f FieldSpec) error {
	if !isIdent(f.Embeddable) {
		return fmt.Errorf("maker: %q is not a valid embeddable name", f.Embeddable)
	}
	if _, err := os.Stat(w.path(f.Embeddable)); err != nil {
		return fmt.Errorf("maker: embeddable %s does not exist; create it with make:embeddable", f.Embeddable)
	}
	if err := w.checkFree(entity, f.Name); err != nil {
		return err
	}
	tags := []string{"embedded"}
	switch {
	case f.Prefix == "-":
		tags = append(tags, "noprefix")
	case f.Prefix != "":
		tags = append(tags, "prefix:"+f.Prefix)
	}
	if err := w.file(entity).AddField(entity, w.fieldName(f.Name)+" "+f.Embeddable+tag(tags), ""); err != nil {
		return err
	}
	if w.style == StyleGo {
		return nil
	}
	return w.accessors(entity, f.Name, f.Embeddable)
}

// MakeFixtures writes a fixture file into dir and returns its path.
func MakeFixtures(dir, name string) (string, error) {
	if !isIdent(name) {
		return "", fmt.Errorf("maker: %q is not a valid fixture name", name)
	}
	path := filepath.Join(dir, mapping.ToSnake(name)+".go")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("maker: %s already exists", path)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	pkg := (&writer{mk: &Maker{Dir: dir}}).packageName()
	src := fmt.Sprintf(`package %[1]s

import (
	"context"

	fx %[2]q
)

func init() {
	fx.Register(fx.Fixture{
		Name: %[3]q,
		// DependsOn: []string{"OtherFixtures"},
		// Groups:    []string{"demo"},
		Load: func(ctx context.Context, f *fx.Context) error {
			// product := (&entity.Product{}).SetName("Keyboard")
			// f.AddReference("product-keyboard", product)
			// return f.EM.Persist(product)
			return nil
		},
	})
}
`, pkg, importPath()+"/fixtures", name)
	f, err := astedit.Parse(path, []byte(src))
	if err != nil {
		return "", err
	}
	return path, f.Save()
}

// EnsureBlankImport adds `import _ "path"` to a Go file (e.g. the console
// main) if it is missing.
func EnsureBlankImport(file, path string) (bool, error) {
	f, err := astedit.Open(file)
	if err != nil {
		return false, err
	}
	src, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	if strings.Contains(string(src), fmt.Sprintf("%q", path)) {
		return false, nil
	}
	f.AddImport(path, "_")
	return true, f.Save()
}
