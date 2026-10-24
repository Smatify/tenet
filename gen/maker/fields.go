package maker

import (
	"fmt"
	"slices"
	"strings"

	"github.com/smatify/smorm/internal/brand"
)

type scalar struct {
	goType   string
	tags     []string
	imports  []string
	pointer  bool // nullable values become pointers
	nullable bool // nullable via tag (maps, byte slices)
}

func scalarFor(f FieldSpec) (scalar, error) {
	switch f.Type {
	case "string":
		n := f.Length
		if n == 0 {
			n = 255
		}
		return scalar{goType: "string", tags: []string{fmt.Sprintf("length:%d", n)}, pointer: true}, nil
	case "text":
		return scalar{goType: "string", tags: []string{"type:text"}, pointer: true}, nil
	case "integer":
		return scalar{goType: "int32", pointer: true}, nil
	case "smallint":
		return scalar{goType: "int16", pointer: true}, nil
	case "bigint":
		return scalar{goType: "int64", pointer: true}, nil
	case "boolean":
		return scalar{goType: "bool", pointer: true}, nil
	case "float":
		return scalar{goType: "float64", pointer: true}, nil
	case "decimal":
		p, s := f.Precision, f.Scale
		if p == 0 {
			p, s = 10, 2
		}
		return scalar{goType: "string", tags: []string{"type:decimal", fmt.Sprintf("precision:%d", p), fmt.Sprintf("scale:%d", s)}, pointer: true}, nil
	case "datetime":
		return scalar{goType: "time.Time", imports: []string{"time"}, pointer: true}, nil
	case "datetimetz", "date", "time":
		return scalar{goType: "time.Time", tags: []string{"type:" + f.Type}, imports: []string{"time"}, pointer: true}, nil
	case "json":
		return scalar{goType: "map[string]any", nullable: true}, nil
	case "uuid":
		return scalar{goType: "uuid.UUID", imports: []string{importPath() + "/uuid"}, pointer: true}, nil
	case "binary":
		return scalar{goType: "[]byte", nullable: true}, nil
	}
	return scalar{}, fmt.Errorf("maker: unknown field type %q (one of %s)", f.Type, strings.Join(FieldTypes, ", "))
}

func tag(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return " `" + brand.TagKey + ":\"" + strings.Join(parts, ",") + "\"`"
}

func (w *writer) addField(entity string, f FieldSpec) error {
	if !isIdent(f.Name) {
		return fmt.Errorf("maker: %q is not a valid property name", f.Name)
	}
	if err := w.checkFree(entity, f.Name); err != nil {
		return err
	}
	s, err := scalarFor(f)
	if err != nil {
		return err
	}
	file := w.file(entity)
	for _, imp := range s.imports {
		file.AddImport(imp, "")
	}
	typ := s.goType
	tags := slices.Clone(s.tags)
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
	name := w.fieldName(f.Name)
	if err := file.AddField(entity, name+" "+typ+tag(tags), ""); err != nil {
		return err
	}
	if w.style == StyleGo {
		return nil
	}
	return w.accessors(entity, f.Name, typ)
}

// method is a declaration to add, named for skipping existing methods.
type method struct{ name, src, imp string }

// addMethods adds methods to entity, except those that exist already when
// the writer completes hand-written entities.
func (w *writer) addMethods(entity string, methods ...method) error {
	var src strings.Builder
	f := w.file(entity)
	for _, m := range methods {
		if w.existing[entity+"."+m.name] {
			continue
		}
		if m.imp != "" {
			f.AddImport(m.imp, "")
		}
		src.WriteString(m.src + "\n")
		w.added = append(w.added, entity+"."+m.name)
	}
	if src.Len() == 0 {
		return nil
	}
	return f.AddDecls(src.String())
}

// accessors adds a Doctrine style getter and fluent setter.
func (w *writer) accessors(entity, prop, typ string) error {
	r, field, up := recv(entity), lower(prop), upper(prop)
	args := []any{r, field, up, entity, typ, root, param(r)}
	return w.addMethods(entity,
		method{name: up, src: fmt.Sprintf(`// %[3]s returns the %[2]s.
func (%[1]s *%[4]s) %[3]s() %[5]s {
	%[6]s.Touch(%[1]s)
	return %[1]s.%[2]s
}
`, args...)},
		method{name: "Set" + up, src: fmt.Sprintf(`// Set%[3]s sets the %[2]s.
func (%[1]s *%[4]s) Set%[3]s(%[7]s %[5]s) *%[4]s {
	%[6]s.Touch(%[1]s)
	%[1]s.%[2]s = %[7]s
	return %[1]s
}
`, args...)})
}

func (w *writer) addRelation(entity string, f FieldSpec) error {
	if !isIdent(f.Name) || !isIdent(f.Target) || !strings.EqualFold(f.Target[:1], strings.ToUpper(f.Target[:1])) {
		return fmt.Errorf("maker: invalid relation %q -> %q", f.Name, f.Target)
	}
	if f.Target[0] < 'A' || f.Target[0] > 'Z' {
		return fmt.Errorf("maker: target %q must be an entity name in UpperCamelCase", f.Target)
	}
	if _, err := w.ensureEntity(f.Target, EntitySpec{Name: f.Target}); err != nil {
		return err
	}
	switch f.Relation {
	case ManyToOne:
		return w.manyToOne(entity, f)
	case OneToMany:
		inv := f.Inverse
		if inv == "" {
			inv = lower(entity)
		}
		return w.manyToOne(f.Target, FieldSpec{Name: inv, Relation: ManyToOne, Target: entity, Inverse: f.Name,
			Nullable: f.Nullable, OrphanRemoval: f.OrphanRemoval, Cascade: f.Cascade, OnDelete: f.OnDelete})
	case ManyToMany:
		return w.manyToMany(entity, f)
	case OneToOne:
		return w.oneToOne(entity, f)
	}
	return fmt.Errorf("maker: unknown relation type %q (manyToOne, oneToMany, manyToMany, oneToOne)", f.Relation)
}

func cascade(tags []string, f FieldSpec) []string {
	if len(f.Cascade) == 0 {
		return tags
	}
	return append(tags, "cascade:"+strings.Join(f.Cascade, "|"))
}

func onDelete(tags []string, f FieldSpec) []string {
	if f.OnDelete == "" {
		return tags
	}
	return append(tags, "onDelete:"+f.OnDelete)
}

func (w *writer) toOneType(target string) string {
	if w.style == StyleGo {
		return root + ".Ref[" + target + "]"
	}
	return "*" + target
}

func (w *writer) manyToOne(owner string, f FieldSpec) error {
	if err := w.checkFree(owner, f.Name); err != nil {
		return err
	}
	if f.Inverse != "" {
		if err := w.checkFree(f.Target, f.Inverse); err != nil {
			return err
		}
	}
	tags := []string{"manyToOne"}
	if f.Inverse != "" {
		tags = append(tags, "inversedBy:"+w.fieldName(f.Inverse))
	}
	if !f.Nullable {
		tags = append(tags, "notnull")
	}
	tags = onDelete(tags, f)
	if err := w.file(owner).AddField(owner, w.fieldName(f.Name)+" "+w.toOneType(f.Target)+tag(tags), ""); err != nil {
		return err
	}
	if w.style == StyleDoctrine {
		if err := w.accessors(owner, f.Name, "*"+f.Target); err != nil {
			return err
		}
	}
	if f.Inverse == "" {
		return nil
	}
	invTags := cascade([]string{"oneToMany", "mappedBy:" + w.fieldName(f.Name)}, f)
	if f.OrphanRemoval {
		invTags = append(invTags, "orphanRemoval")
	}
	if err := w.collection(f.Target, f.Inverse, owner, invTags); err != nil {
		return err
	}
	if w.style == StyleGo {
		return nil
	}
	unset := fmt.Sprintf("\t\tif %[1]s.%[2]s() == %[3]s {\n\t\t\t%[1]s.Set%[2]s(nil)\n\t\t}\n", "item", upper(f.Name), recv(f.Target))
	if f.OrphanRemoval {
		unset = ""
	}
	return w.collectionMethods(f.Target, f.Inverse, owner,
		fmt.Sprintf("\t\titem.Set%s(%s)\n", upper(f.Name), recv(f.Target)), unset)
}

func (w *writer) collection(entity, prop, target string, tags []string) error {
	return w.file(entity).AddField(entity, w.fieldName(prop)+" "+root+".Collection["+target+"]"+tag(tags), "")
}

// collectionMethods adds Items(), AddItem(), RemoveItem() and SetItems()
// with hooks that keep the other side in sync.
func (w *writer) collectionMethods(entity, prop, target, onAdd, onRemove string) error {
	r, field, up, one := recv(entity), lower(prop), upper(prop), upper(singular(prop))
	if one == up {
		one = up + "Item"
	}
	args := []any{r, field, up, entity, target, one, onAdd, root, onRemove}
	return w.addMethods(entity,
		method{name: up, src: fmt.Sprintf(`// %[3]s returns the %[2]s.
func (%[1]s *%[4]s) %[3]s() []*%[5]s {
	%[8]s.Touch(%[1]s)
	return %[1]s.%[2]s.All()
}
`, args...)},
		method{name: "Add" + one, src: fmt.Sprintf(`// Add%[6]s adds item to the %[2]s.
func (%[1]s *%[4]s) Add%[6]s(item *%[5]s) *%[4]s {
	%[8]s.Touch(%[1]s)
	if !%[1]s.%[2]s.Contains(item) {
		%[1]s.%[2]s.Add(item)
%[7]s	}
	return %[1]s
}
`, args...)},
		method{name: "Remove" + one, src: fmt.Sprintf(`// Remove%[6]s removes item from the %[2]s.
func (%[1]s *%[4]s) Remove%[6]s(item *%[5]s) *%[4]s {
	%[8]s.Touch(%[1]s)
	if %[1]s.%[2]s.Remove(item) {
%[9]s	}
	return %[1]s
}
`, args...)},
		method{name: "Set" + up, imp: "slices", src: fmt.Sprintf(`// Set%[3]s replaces the %[2]s with items.
func (%[1]s *%[4]s) Set%[3]s(items []*%[5]s) *%[4]s {
	for _, item := range %[1]s.%[3]s() {
		if !slices.Contains(items, item) {
			%[1]s.Remove%[6]s(item)
		}
	}
	for _, item := range items {
		%[1]s.Add%[6]s(item)
	}
	return %[1]s
}
`, args...)})
}

func (w *writer) manyToMany(owner string, f FieldSpec) error {
	if err := w.checkFree(owner, f.Name); err != nil {
		return err
	}
	if f.Inverse != "" {
		if err := w.checkFree(f.Target, f.Inverse); err != nil {
			return err
		}
	}
	tags := []string{"manyToMany"}
	if f.Inverse != "" {
		tags = append(tags, "inversedBy:"+w.fieldName(f.Inverse))
	}
	if f.JoinTable != "" {
		tags = append(tags, "joinTable:"+f.JoinTable)
	}
	tags = cascade(tags, f)
	if err := w.collection(owner, f.Name, f.Target, tags); err != nil {
		return err
	}
	if w.style == StyleDoctrine {
		if err := w.collectionMethods(owner, f.Name, f.Target, "", ""); err != nil {
			return err
		}
	}
	if f.Inverse == "" {
		return nil
	}
	if err := w.collection(f.Target, f.Inverse, owner, []string{"manyToMany", "mappedBy:" + w.fieldName(f.Name)}); err != nil {
		return err
	}
	if w.style == StyleGo {
		return nil
	}
	ownerOne := upper(singular(f.Name))
	if ownerOne == upper(f.Name) {
		ownerOne += "Item"
	}
	r := recv(f.Target)
	return w.collectionMethods(f.Target, f.Inverse, owner,
		fmt.Sprintf("\t\titem.Add%s(%s)\n", ownerOne, r), fmt.Sprintf("\t\titem.Remove%s(%s)\n", ownerOne, r))
}

func (w *writer) oneToOne(owner string, f FieldSpec) error {
	if err := w.checkFree(owner, f.Name); err != nil {
		return err
	}
	if f.Inverse != "" {
		if err := w.checkFree(f.Target, f.Inverse); err != nil {
			return err
		}
	}
	tags := []string{"oneToOne"}
	if f.Inverse != "" {
		tags = append(tags, "inversedBy:"+w.fieldName(f.Inverse))
	}
	if !f.Nullable {
		tags = append(tags, "notnull")
	}
	tags = onDelete(cascade(tags, f), f)
	if err := w.file(owner).AddField(owner, w.fieldName(f.Name)+" "+w.toOneType(f.Target)+tag(tags), ""); err != nil {
		return err
	}
	if w.style == StyleDoctrine {
		if err := w.accessors(owner, f.Name, "*"+f.Target); err != nil {
			return err
		}
	}
	if f.Inverse == "" {
		return nil
	}
	invTags := []string{"oneToOne", "mappedBy:" + w.fieldName(f.Name)}
	if f.OrphanRemoval {
		invTags = append(invTags, "orphanRemoval")
	}
	if err := w.file(f.Target).AddField(f.Target, w.fieldName(f.Inverse)+" "+w.toOneType(owner)+tag(invTags), ""); err != nil {
		return err
	}
	if w.style == StyleGo {
		return nil
	}
	return w.accessors(f.Target, f.Inverse, "*"+owner)
}
