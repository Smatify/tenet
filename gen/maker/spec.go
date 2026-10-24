package maker

import (
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/smatify/smorm/gen/astedit"
	"github.com/smatify/smorm/internal/brand"
	"github.com/smatify/smorm/mapping"
)

// Style selects how generated entities expose their data.
type Style string

// Styles.
const (
	StyleDoctrine Style = "doctrine" // unexported fields, getters/setters, lazy loading via Touch
	StyleGo       Style = "go"       // exported fields, Ref[T] for to-one associations
)

// Relation kinds offered by make:entity.
const (
	ManyToOne  = "manyToOne"
	OneToMany  = "oneToMany"
	ManyToMany = "manyToMany"
	OneToOne   = "oneToOne"
)

// FieldTypes lists the field types make:entity offers.
var FieldTypes = []string{"string", "text", "integer", "smallint", "bigint", "boolean", "float", "decimal",
	"datetime", "datetimetz", "date", "time", "json", "uuid", "binary", "relation", "embedded"}

// FieldSpec describes a field to add.
type FieldSpec struct {
	Name      string // property name as typed, e.g. "title" or "publishedAt"
	Type      string // one of FieldTypes
	Length    int    // string length (default 255)
	Precision int    // decimal
	Scale     int    // decimal
	Nullable  bool
	Unique    bool

	Relation      string // relation kind when Type == "relation"
	Target        string // target entity name
	Inverse       string // property on the target ("" = unidirectional)
	OrphanRemoval bool

	Embeddable string // struct name when Type == "embedded"
	Prefix     string // column prefix ("" = default, "-" = none)
}

// EntitySpec describes an entity to create or extend.
type EntitySpec struct {
	Name       string
	Table      string // default snake_case of Name
	Style      Style
	UUID       bool // UUIDv7 identifier instead of auto increment
	Timestamps bool
	Fields     []FieldSpec
}

// Maker writes entity files into Dir.
type Maker struct {
	Dir   string
	Style Style
}

var root = brand.Name // package qualifier of the root package in generated code

const rootImport = "github.com/smatify/" // + brand.Name

func importPath() string { return rootImport + brand.Name }

// Apply creates or extends the entity and returns the files it wrote.
func (mk *Maker) Apply(spec EntitySpec) ([]string, error) {
	if !isIdent(spec.Name) || !unicode.IsUpper(rune(spec.Name[0])) {
		return nil, fmt.Errorf("maker: %q is not a valid entity name (use UpperCamelCase)", spec.Name)
	}
	if spec.Style == "" {
		spec.Style = mk.Style
	}
	if spec.Style == "" {
		spec.Style = StyleDoctrine
	}
	for _, f := range spec.Fields {
		for _, name := range []string{f.Name, f.Inverse} {
			if token.IsKeyword(name) {
				return nil, fmt.Errorf("maker: %q is a Go keyword and cannot be a property name; choose another one (e.g. %q)", name, lower(spec.Name)+upper(name))
			}
		}
	}
	w := &writer{mk: mk, style: spec.Style, files: map[string]*astedit.File{}}
	if _, err := w.ensureEntity(spec.Name, spec); err != nil {
		return nil, err
	}
	for _, f := range spec.Fields {
		var err error
		switch f.Type {
		case "relation":
			err = w.addRelation(spec.Name, f)
		case "embedded":
			err = w.addEmbedded(spec.Name, f)
		default:
			err = w.addField(spec.Name, f)
		}
		if err != nil {
			return nil, err
		}
	}
	return w.save()
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(unicode.IsLetter(r) || r == '_' || (i > 0 && unicode.IsDigit(r))) {
			return false
		}
	}
	return true
}

type writer struct {
	mk      *Maker
	style   Style
	files   map[string]*astedit.File
	created []string
	paths   map[string]string // entity → file declaring it
}

// path returns the file declaring entity: the file in Dir that declares it
// (files may be renamed, e.g. after their table), else <snake name>.go.
func (w *writer) path(entity string) string {
	if p, ok := w.paths[entity]; ok {
		return p
	}
	matches, _ := filepath.Glob(filepath.Join(w.mk.Dir, "*.go"))
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		if f, err := astedit.Open(m); err == nil && f.Struct(entity) != nil {
			w.remember(entity, m)
			return m
		}
	}
	return filepath.Join(w.mk.Dir, mapping.ToSnake(entity)+".go")
}

func (w *writer) remember(entity, path string) {
	if w.paths == nil {
		w.paths = map[string]string{}
	}
	w.paths[entity] = path
}

func (w *writer) packageName() string {
	matches, _ := filepath.Glob(filepath.Join(w.mk.Dir, "*.go"))
	for _, m := range matches {
		if f, err := astedit.Open(m); err == nil && !strings.HasSuffix(f.Package(), "_test") {
			return f.Package()
		}
	}
	return strings.ReplaceAll(filepath.Base(w.mk.Dir), "-", "_")
}

// ensureEntity opens the entity file, creating the skeleton (and the
// repository) if needed. It reports whether the entity was created.
func (w *writer) ensureEntity(name string, spec EntitySpec) (bool, error) {
	path := w.path(name)
	if _, ok := w.files[path]; ok {
		return false, nil
	}
	if _, err := os.Stat(path); err == nil {
		f, err := astedit.Open(path)
		if err != nil {
			return false, err
		}
		if f.Struct(name) == nil {
			return false, fmt.Errorf("maker: %s exists but does not declare %s", path, name)
		}
		w.files[path] = f
		return false, nil
	}
	if err := os.MkdirAll(w.mk.Dir, 0o755); err != nil {
		return false, err
	}
	pkg := w.packageName()
	table := spec.Table
	if table == "" {
		table = mapping.ToSnake(name)
	}
	// New files are named after their table (media_demo.go), which keeps
	// entities of one module together.
	path = filepath.Join(w.mk.Dir, table+".go")
	w.remember(name, path)
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport %q\n\n// %s is an entity.\ntype %s struct {\n", pkg, importPath(), name, name)
	fmt.Fprintf(&b, "\t%s.Entity `%s:\"table:%s\"`\n", root, brand.TagKey, table)
	switch {
	case w.style == StyleGo && spec.UUID:
		fmt.Fprintf(&b, "\tID uuid.UUID\n")
	case w.style == StyleGo:
		fmt.Fprintf(&b, "\tID int64\n")
	case spec.UUID:
		fmt.Fprintf(&b, "\t%s.UUIDID\n", root)
	default:
		fmt.Fprintf(&b, "\t%s.IntID\n", root)
	}
	if spec.Timestamps {
		fmt.Fprintf(&b, "\t%s.Timestamps\n", root)
	}
	b.WriteString("}\n")
	f, err := astedit.Parse(path, []byte(b.String()))
	if err != nil {
		return false, err
	}
	if w.style == StyleGo && spec.UUID {
		f.AddImport(importPath()+"/uuid", "")
	}
	w.files[path] = f
	w.created = append(w.created, path)
	if err := w.repository(name, pkg, strings.TrimSuffix(path, ".go")+"_repository.go"); err != nil {
		return false, err
	}
	return true, nil
}

func (w *writer) repository(name, pkg, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	src := fmt.Sprintf(`package %[1]s

import %[2]q

// %[3]sRepository holds custom queries for %[3]s.
type %[3]sRepository struct {
	*%[4]s.Repository[%[3]s]
}

// New%[3]sRepository returns the repository of %[3]s.
func New%[3]sRepository(em *%[4]s.EntityManager) *%[3]sRepository {
	return &%[3]sRepository{%[4]s.RepositoryFor[%[3]s](em)}
}
`, pkg, importPath(), name, root)
	f, err := astedit.Parse(path, []byte(src))
	if err != nil {
		return err
	}
	w.files[path] = f
	w.created = append(w.created, path)
	return nil
}

func (w *writer) file(entity string) *astedit.File { return w.files[w.path(entity)] }

func (w *writer) save() ([]string, error) {
	var out []string
	for path, f := range w.files {
		if err := f.Save(); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, nil
}

// lower returns lowerCamelCase, upper UpperCamelCase.
func lower(s string) string {
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func upper(s string) string {
	if strings.EqualFold(s, "id") {
		return "ID"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// singular derives the element name of a collection ("categories" -> "category").
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "xes"), strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "shes"),
		strings.HasSuffix(s, "iases"), strings.HasSuffix(s, "tuses"), strings.HasSuffix(s, "nuses"), strings.HasSuffix(s, "puses"), strings.HasSuffix(s, "ruses"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "oes") && len(s) > 4 && !strings.HasSuffix(s, "shoes"):
		return s[:len(s)-2] // heroes, vetoes, potatoes
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") && len(s) > 1:
		return s[:len(s)-1]
	}
	return s
}

// plural derives a collection name ("category" -> "categories").
func plural(s string) string {
	switch {
	case strings.HasSuffix(s, "y") && len(s) > 1 && !strings.ContainsRune("aeiou", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}

// fieldName returns the Go field name for a property in the current style.
func (w *writer) fieldName(prop string) string {
	if w.style == StyleGo {
		return upper(prop)
	}
	return lower(prop)
}

func recv(entity string) string { return strings.ToLower(entity[:1]) }

// param names the parameter of a setter; it must differ from the receiver
// (entities starting with V would get "v" twice).
func param(receiver string) string {
	if receiver == "v" {
		return "value"
	}
	return "v"
}

var errExists = errors.New("already exists")

func (w *writer) checkFree(entity, prop string) error {
	f := w.file(entity)
	if f.HasField(entity, prop) || f.HasMethod(entity, upper(prop)) {
		return fmt.Errorf("maker: %s.%s %w", entity, prop, errExists)
	}
	return nil
}
