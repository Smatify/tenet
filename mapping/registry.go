package mapping

import (
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smatify/smorm/internal/brand"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/types"
	"github.com/smatify/smorm/uuid"
)

// Registry parses and caches entity metadata. It is safe for concurrent use.
type Registry struct {
	mu       sync.Mutex
	naming   NamingStrategy
	types    *types.Registry
	entities map[reflect.Type]*Entity
}

// Option configures a Registry.
type Option func(*Registry)

// WithNaming sets the naming strategy.
func WithNaming(n NamingStrategy) Option { return func(r *Registry) { r.naming = n } }

// WithTypes sets the type registry.
func WithTypes(t *types.Registry) Option { return func(r *Registry) { r.types = t } }

// NewRegistry returns an empty registry.
func NewRegistry(opts ...Option) *Registry {
	r := &Registry{naming: SnakeCase{}, types: types.Default, entities: map[reflect.Type]*Entity{}}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Default is the process wide registry.
var Default = NewRegistry()

// Types returns the type registry.
func (r *Registry) Types() *types.Registry { return r.types }

// Naming returns the naming strategy.
func (r *Registry) Naming() NamingStrategy { return r.naming }

// Get returns the metadata for t (struct or pointer to struct).
func (r *Registry) Get(t reflect.Type) (*Entity, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.get(t)
}

// For returns the metadata for T.
func For[T any](r *Registry) (*Entity, error) { return r.Get(reflect.TypeFor[T]()) }

// Register parses the given entities (values or pointers) eagerly.
func (r *Registry) Register(entities ...any) error {
	for _, e := range entities {
		if _, err := r.Get(reflect.TypeOf(e)); err != nil {
			return err
		}
	}
	return nil
}

// All returns every parsed entity sorted by name.
func (r *Registry) All() []*Entity {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Entity, 0, len(r.entities))
	for _, e := range r.entities {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b *Entity) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// IsEntity reports whether t (or *t) embeds the entity header.
func IsEntity(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	if _, ok := findHeader(t); ok {
		return true
	}
	_, ok := parentField(t)
	return ok
}

// parentField returns the anonymous embedded entity of a subclass.
func parentField(t reflect.Type) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Type != HeaderType && IsEntity(f.Type) {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func findHeader(t reflect.Type) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type == HeaderType {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func (r *Registry) get(t reflect.Type) (*Entity, error) {
	if e, ok := r.entities[t]; ok {
		return e, nil
	}
	e, err := r.parse(t)
	if err != nil {
		return nil, err
	}
	r.entities[t] = e // before resolving, so cycles terminate
	if err := r.resolve(e); err != nil {
		delete(r.entities, t)
		return nil, err
	}
	return e, nil
}

// MappingError describes an invalid mapping.
type MappingError struct {
	Entity string
	Field  string
	Msg    string
}

func (e *MappingError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s.%s: %s", brand.Name, e.Entity, e.Field, e.Msg)
	}
	return fmt.Sprintf("%s: %s: %s", brand.Name, e.Entity, e.Msg)
}

var entityOptions = map[string]bool{"table": true, "repository": true, "comment": true, "sequence": true, "index": true,
	"inheritance": true, "discriminator": true, "discriminatorValue": true, "abstract": true,
	"cache": true, "cacheTTL": true}

var fieldOptions = map[string]bool{
	"column": true, "type": true, "length": true, "precision": true, "scale": true, "fixed": true,
	"nullable": true, "notnull": true, "nullzero": true, "default": true, "comment": true,
	"id": true, "pk": true, "generated": true, "version": true, "unique": true, "index": true,
	"manyToOne": true, "oneToMany": true, "oneToOne": true, "manyToMany": true,
	"mappedBy": true, "inversedBy": true, "joinColumn": true, "referencedColumn": true,
	"onDelete": true, "joinTable": true, "cascade": true, "fetch": true, "orphanRemoval": true, "orderBy": true,
	"embedded": true, "prefix": true, "noprefix": true,
}

func (r *Registry) parse(t reflect.Type) (*Entity, error) {
	if t.Kind() != reflect.Struct {
		return nil, &MappingError{Entity: t.String(), Msg: "entities must be structs"}
	}
	e := &Entity{GoType: t, Name: t.Name()}
	errf := func(field, format string, args ...any) error {
		return &MappingError{Entity: e.Name, Field: field, Msg: fmt.Sprintf(format, args...)}
	}
	hdr, ok := findHeader(t)
	if !ok {
		if pf, isSub := parentField(t); isSub {
			return r.parseSubclass(e, pf)
		}
		return nil, errf("", "not an entity: embed %s.Entity", brand.Name)
	}
	e.HeaderIndex = hdr.Index
	e.Root = e
	tag, err := ParseTag(hdr.Tag.Get(brand.TagKey))
	if err != nil {
		return nil, errf("", "%v", err)
	}
	for _, k := range tag.Keys() {
		if !entityOptions[k] {
			return nil, errf("", "unknown entity tag option %q", k)
		}
	}
	e.Table = tag.Get("table")
	if e.Table == "" {
		e.Table = r.naming.TableName(e.Name)
	}
	e.Repository = tag.Get("repository")
	e.Comment = Unquote(tag.Get("comment"))
	e.Sequence = tag.Get("sequence")
	if tag.Has("cache") || tag.Has("cacheTTL") {
		e.Cacheable = true
		if v := tag.Get("cacheTTL"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d < 0 {
				return nil, errf("", "invalid cacheTTL %q", v)
			}
			e.CacheTTL = d
		}
	}
	switch inh := Inheritance(tag.Get("inheritance")); inh {
	case "":
		if tag.Has("discriminator") || tag.Has("discriminatorValue") {
			return nil, errf("", "discriminator options need inheritance:single or inheritance:joined")
		}
	case SingleTable, Joined:
		e.Inheritance = inh
		e.DiscriminatorColumn = tag.Get("discriminator")
		if e.DiscriminatorColumn == "" {
			e.DiscriminatorColumn = "dtype"
		}
		e.DiscriminatorValue = tag.Get("discriminatorValue")
		if e.DiscriminatorValue == "" {
			e.DiscriminatorValue = r.naming.TableName(e.Name)
		}
		e.Abstract = tag.Has("abstract")
	default:
		return nil, errf("", "unknown inheritance %q (single or joined)", inh)
	}

	if err := r.walk(e, t, nil, embedCtx{}); err != nil {
		return nil, err
	}
	if len(e.ID) == 0 {
		for _, f := range e.Fields {
			if strings.EqualFold(f.Name, "id") {
				f.ID = true
				e.ID = append(e.ID, f)
			}
		}
	}
	if len(e.ID) == 0 {
		return nil, errf("", "no identifier: tag a field with `%s:\"id\"` or name it ID", brand.TagKey)
	}
	for _, f := range e.ID {
		f.Nullable = false
	}
	for _, f := range e.Fields {
		if f.generator == "" {
			continue
		}
		if !f.ID {
			return nil, errf(f.Name, "generated is only allowed on identifier fields")
		}
		e.Generator = f.generator
	}
	if e.Generator == "" {
		e.Generator = GenNone
		if id := e.SingleID(); id != nil {
			switch {
			case id.Type.Name() == types.SmallInt || id.Type.Name() == types.Integer || id.Type.Name() == types.BigInt:
				e.Generator = GenIdentity
			case id.GoType == reflect.TypeFor[uuid.UUID]():
				e.Generator = GenUUIDv7
			}
		}
	}
	if e.Generator == GenSequence && e.Sequence == "" {
		e.Sequence = e.Table + "_seq"
	}
	seen := map[string]string{}
	for _, f := range e.Fields {
		key := strings.ToLower(f.Column)
		if other, dup := seen[key]; dup {
			return nil, errf(f.Name, "column %q is already mapped by %s", f.Column, other)
		}
		seen[key] = f.Name
		if f.Name == "" {
			seen[key] = "association " + f.Assoc.Name
		}
		if !f.Exported && f.Name != "" {
			e.DoctrineStyle = true
		}
		f.Table = e.Table
	}
	if e.Inheritance != "" {
		if col := e.FieldByColumn(e.DiscriminatorColumn); col != nil || e.byColumnHas(e.DiscriminatorColumn) {
			return nil, errf("", "discriminator column %q collides with a mapped field", e.DiscriminatorColumn)
		}
	}
	e.indexSpec = tag.Get("index")
	e.index()
	return e, nil
}

// entityIndexes adds the indexes declared on the entity header, e.g.
// index:subsite|createdAt;stream: groups of fields or to-one associations
// separated by semicolons. They cover columns a field tag cannot reach
// (join columns, embedded timestamps).
func (e *Entity) entityIndexes(v string) error {
	if v == "" {
		return nil
	}
	for _, group := range strings.Split(v, ";") {
		var cols []string
		for _, name := range List(group) {
			col := ""
			for _, f := range e.Fields {
				if f.Name == name || (f.Name == "" && f.Assoc != nil && f.Assoc.Name == name) {
					col = f.Column
				}
			}
			if col == "" {
				return fmt.Errorf("index: unknown field %q", name)
			}
			cols = append(cols, col)
		}
		if len(cols) == 0 {
			return fmt.Errorf("index: empty group in %q", v)
		}
		e.Indexes = append(e.Indexes, &IndexDef{Name: schema.IdentifierName("idx", e.Table, cols), Columns: cols})
	}
	return nil
}

var (
	timeType  = reflect.TypeFor[time.Time]()
	nullTypes = map[reflect.Type]types.Name{
		reflect.TypeFor[sql.NullString]():  types.String,
		reflect.TypeFor[sql.NullInt64]():   types.BigInt,
		reflect.TypeFor[sql.NullInt32]():   types.Integer,
		reflect.TypeFor[sql.NullInt16]():   types.SmallInt,
		reflect.TypeFor[sql.NullBool]():    types.Boolean,
		reflect.TypeFor[sql.NullFloat64](): types.Float,
		reflect.TypeFor[sql.NullTime]():    types.DateTime,
	}
	collectionIface = reflect.TypeFor[CollectionField]()
	refIface        = reflect.TypeFor[RefField]()
)

func isAssocTag(tag Tag) (AssocKind, bool) {
	switch {
	case tag.Has("manyToOne"):
		return ManyToOne, true
	case tag.Has("oneToMany"):
		return OneToMany, true
	case tag.Has("oneToOne"):
		return OneToOne, true
	case tag.Has("manyToMany"):
		return ManyToMany, true
	}
	return 0, false
}

// embedCtx carries the column and Go name prefixes of an embeddable.
type embedCtx struct {
	column string // column prefix, e.g. "billing_"
	name   string // Go path prefix, e.g. "Billing."
	field  string // embedded field name for error messages
}

// walk collects fields of t (and anonymous embedded structs) into e.
func (r *Registry) walk(e *Entity, t reflect.Type, prefix []int, emb embedCtx) error {
	for i := range t.NumField() {
		if err := r.walkField(e, t, i, prefix, emb); err != nil {
			return err
		}
	}
	return nil
}

// walkField maps field i of t.
func (r *Registry) walkField(e *Entity, t reflect.Type, i int, prefix []int, emb embedCtx) error {
	sf := t.Field(i)
	index := append(slices.Clone(prefix), i)
	if sf.Anonymous && sf.Type == HeaderType {
		return nil
	}
	raw, hasTag := sf.Tag.Lookup(brand.TagKey)
	if raw == "-" {
		return nil
	}
	tag, err := ParseTag(raw)
	if err != nil {
		return &MappingError{Entity: e.Name, Field: sf.Name, Msg: err.Error()}
	}
	for _, k := range tag.Keys() {
		if !fieldOptions[k] {
			return &MappingError{Entity: e.Name, Field: sf.Name, Msg: fmt.Sprintf("unknown tag option %q", k)}
		}
	}
	ft := sf.Type
	if sf.Anonymous && ft.Kind() == reflect.Struct && ft != timeType && !types.IsValuerScanner(ft) && !hasTag {
		if err := r.walk(e, ft, index, emb); err != nil {
			return err
		}
		return nil
	}
	if tag.Has("embedded") {
		if ft.Kind() != reflect.Struct || IsEntity(ft) {
			return &MappingError{Entity: e.Name, Field: emb.name + sf.Name, Msg: "embedded fields must be (non-entity) struct values"}
		}
		sub := embedCtx{column: emb.column, name: emb.name + sf.Name + ".", field: emb.name + sf.Name}
		switch {
		case tag.Has("noprefix"):
		case tag.Get("prefix") != "":
			sub.column += tag.Get("prefix")
		default:
			sub.column += r.naming.ColumnName(sf.Name) + "_"
		}
		if err := r.walk(e, ft, index, sub); err != nil {
			return err
		}
		return nil
	}
	if tag.Has("prefix") || tag.Has("noprefix") {
		return &MappingError{Entity: e.Name, Field: emb.name + sf.Name, Msg: "prefix and noprefix need embedded"}
	}
	if _, ok := isAssocTag(tag); ok || isEntityContainer(ft) {
		if emb.field != "" {
			return &MappingError{Entity: e.Name, Field: emb.name + sf.Name, Msg: "associations inside embeddables are not supported"}
		}
	}
	if kind, ok := isAssocTag(tag); ok {
		if err := r.parseAssoc(e, sf, index, tag, kind); err != nil {
			return err
		}
		return nil
	}
	if isEntityContainer(ft) {
		if err := r.parseAssoc(e, sf, index, tag, 0); err != nil {
			return err
		}
		return nil
	}
	if emb.field != "" && (tag.Has("id") || tag.Has("pk")) {
		return &MappingError{Entity: e.Name, Field: emb.name + sf.Name, Msg: "identifiers inside embeddables are not supported"}
	}
	f, err := r.parseField(e, sf, index, tag, emb)
	if err != nil {
		return err
	}
	e.Fields = append(e.Fields, f)
	return nil
}

func isEntityContainer(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer && IsEntity(t.Elem()) {
		return true
	}
	return t.Implements(collectionIface) || t.Implements(refIface)
}

func (r *Registry) parseField(e *Entity, sf reflect.StructField, index []int, tag Tag, emb embedCtx) (*Field, error) {
	errf := func(format string, args ...any) error {
		return &MappingError{Entity: e.Name, Field: sf.Name, Msg: fmt.Sprintf(format, args...)}
	}
	f := &Field{Name: emb.name + sf.Name, Index: index, GoType: sf.Type, Exported: sf.IsExported()}
	f.Column = tag.Get("column")
	if f.Column == "" {
		f.Column = r.naming.ColumnName(sf.Name)
	}
	f.Column = emb.column + f.Column
	var typeName types.Name
	if tn := tag.Get("type"); tn != "" {
		typeName = types.Name(tn)
	} else if n, ok := nullTypes[sf.Type]; ok {
		typeName = n
	}
	if typeName != "" {
		t, ok := r.types.Lookup(typeName)
		if !ok {
			return nil, errf("unknown type %q", typeName)
		}
		f.Type = t
	} else {
		t, ok := r.types.Infer(sf.Type)
		if !ok {
			return nil, errf("cannot infer column type of %s; add `type:...` to the tag", sf.Type)
		}
		f.Type = t
	}
	intOpt := func(key string) (int, error) {
		v := tag.Get(key)
		if v == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, errf("%s must be a non-negative integer, got %q", key, v)
		}
		return n, nil
	}
	var err error
	if f.Options.Length, err = intOpt("length"); err != nil {
		return nil, err
	}
	if f.Options.Precision, err = intOpt("precision"); err != nil {
		return nil, err
	}
	if f.Options.Scale, err = intOpt("scale"); err != nil {
		return nil, err
	}
	f.Options.Fixed = tag.Has("fixed")
	_, isNullType := nullTypes[sf.Type]
	f.Nullable = sf.Type.Kind() == reflect.Pointer || isNullType
	if tag.Has("nullable") && tag.Has("notnull") {
		return nil, errf("nullable and notnull are mutually exclusive")
	}
	if tag.Has("nullable") {
		f.Nullable = true
	}
	if tag.Has("notnull") {
		f.Nullable = false
	}
	f.NullZero = tag.Has("nullzero")
	if f.NullZero {
		f.Nullable = true
	}
	if tag.Has("default") {
		d := tag.Get("default")
		f.Default = &d
	}
	f.Comment = Unquote(tag.Get("comment"))
	if tag.Has("id") || tag.Has("pk") {
		f.ID = true
		e.ID = append(e.ID, f)
	}
	if g := tag.Get("generated"); g != "" {
		switch Generator(g) {
		case GenNone, GenIdentity, GenSequence, GenUUIDv7, GenUUIDv4:
			f.generator = Generator(g)
		default:
			return nil, errf("unknown generator %q", g)
		}
	}
	if tag.Has("version") {
		if e.Version != nil {
			return nil, errf("entity already has version field %s", e.Version.Name)
		}
		f.Version = true
		e.Version = f
	}
	for _, kind := range []string{"unique", "index"} {
		if !tag.Has(kind) {
			continue
		}
		unique := kind == "unique"
		group := tag.Get(kind)
		if group == "" {
			prefix := "idx"
			if unique {
				prefix = "uniq"
			}
			e.Indexes = append(e.Indexes, &IndexDef{Name: schema.IdentifierName(prefix, e.Table, []string{f.Column}), Columns: []string{f.Column}, Unique: unique})
			continue
		}
		var def *IndexDef
		for _, d := range e.Indexes {
			if d.Name == group {
				def = d
			}
		}
		if def == nil {
			def = &IndexDef{Name: group, Unique: unique}
			e.Indexes = append(e.Indexes, def)
		} else if def.Unique != unique {
			return nil, errf("index %q is declared both unique and non-unique", group)
		}
		def.Columns = append(def.Columns, f.Column)
	}
	return f, nil
}

func (r *Registry) parseAssoc(e *Entity, sf reflect.StructField, index []int, tag Tag, kind AssocKind) error {
	errf := func(format string, args ...any) error {
		return &MappingError{Entity: e.Name, Field: sf.Name, Msg: fmt.Sprintf(format, args...)}
	}
	a := &Association{Name: sf.Name, Kind: kind, Index: index, GoType: sf.Type, Source: e, Exported: sf.IsExported()}
	t := sf.Type
	switch {
	case t.Implements(collectionIface):
		a.Container = ContainerCollection
		a.TargetType = reflect.Zero(t).Interface().(CollectionField).CollectionElem()
	case t.Implements(refIface):
		a.Container = ContainerRef
		a.TargetType = reflect.Zero(t).Interface().(RefField).RefElem()
	case t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct:
		a.Container = ContainerPointer
		a.TargetType = t.Elem()
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Pointer && t.Elem().Elem().Kind() == reflect.Struct:
		a.Container = ContainerSlice
		a.TargetType = t.Elem().Elem()
	default:
		return errf("unsupported association type %s", t)
	}
	for a.TargetType.Kind() == reflect.Pointer {
		a.TargetType = a.TargetType.Elem()
	}
	toMany := a.Container == ContainerCollection || a.Container == ContainerSlice
	if a.Kind == 0 {
		switch {
		case !toMany:
			a.Kind = ManyToOne
		case tag.Has("mappedBy"):
			a.Kind = OneToMany
		default:
			return errf("collections need an explicit oneToMany (with mappedBy) or manyToMany tag")
		}
	}
	if a.Kind.IsToOne() == toMany {
		return errf("%s cannot be held in %s", a.Kind, t)
	}
	a.MappedBy = tag.Get("mappedBy")
	a.InversedBy = tag.Get("inversedBy")
	if a.MappedBy != "" && a.InversedBy != "" {
		return errf("mappedBy and inversedBy are mutually exclusive")
	}
	switch a.Kind {
	case ManyToOne:
		if a.MappedBy != "" {
			return errf("manyToOne is always the owning side; use inversedBy")
		}
		a.Owning = true
	case OneToMany:
		if a.MappedBy == "" {
			return errf("oneToMany requires mappedBy (the manyToOne field on the target)")
		}
	default:
		a.Owning = a.MappedBy == ""
	}
	for _, c := range List(tag.Get("cascade")) {
		switch c {
		case "persist":
			a.Cascade |= CascadePersist
		case "remove":
			a.Cascade |= CascadeRemove
		case "refresh":
			a.Cascade |= CascadeRefresh
		case "detach":
			a.Cascade |= CascadeDetach
		case "all":
			a.Cascade |= CascadeAll
		default:
			return errf("unknown cascade %q", c)
		}
	}
	a.Fetch = FetchLazy
	if f := tag.Get("fetch"); f != "" {
		switch Fetch(f) {
		case FetchLazy, FetchEager, FetchExtraLazy:
			a.Fetch = Fetch(f)
		default:
			return errf("unknown fetch mode %q", f)
		}
	}
	if a.Container == ContainerSlice {
		a.Fetch = FetchEager // plain slices cannot load lazily
	}
	a.OrphanRemoval = tag.Has("orphanRemoval")
	if a.OrphanRemoval && (a.Kind == ManyToOne || a.Kind == ManyToMany) {
		return errf("orphanRemoval is only valid on oneToMany and oneToOne")
	}
	a.OrderBy = tag.Get("orderBy")
	if a.Owning && a.Kind.IsToOne() {
		jc := JoinColumn{
			Name:       tag.Get("joinColumn"),
			Referenced: tag.Get("referencedColumn"),
			Nullable:   !tag.Has("notnull"),
			OnDelete:   strings.ToUpper(strings.ReplaceAll(tag.Get("onDelete"), "_", " ")),
		}
		a.JoinColumns = []JoinColumn{jc}
		if tag.Has("nullable") {
			a.JoinColumns[0].Nullable = true
		}
	}
	if a.Owning && a.Kind == ManyToMany {
		a.JoinTable = &JoinTable{Name: tag.Get("joinTable")}
	}
	if !a.Owning && (tag.Has("joinColumn") || tag.Has("joinTable")) {
		return errf("join columns belong to the owning side")
	}
	e.Assocs = append(e.Assocs, a)
	return nil
}

// resolve links associations to their target metadata and completes join
// columns once target identifiers are known.
func (r *Registry) resolve(e *Entity) error {
	for _, a := range e.Assocs {
		if a.Inherited {
			continue // resolved on the parent
		}
		errf := func(format string, args ...any) error {
			return &MappingError{Entity: e.Name, Field: a.Name, Msg: fmt.Sprintf(format, args...)}
		}
		target, err := r.get(a.TargetType)
		if err != nil {
			return err
		}
		a.Target = target
		if a.Owning && a.Kind.IsToOne() {
			id := target.SingleID()
			if id == nil {
				return errf("target %s has a composite identifier; composite join columns are not supported yet", target.Name)
			}
			jc := &a.JoinColumns[0]
			if jc.Referenced == "" {
				jc.Referenced = id.Column
			}
			ref := target.FieldByColumn(jc.Referenced)
			if ref == nil {
				return errf("referenced column %q does not exist on %s", jc.Referenced, target.Name)
			}
			if jc.Name == "" {
				jc.Name = r.naming.JoinColumnName(a.Name, jc.Referenced)
			}
			fk := &Field{Table: e.Table, Column: jc.Name, Type: ref.Type, Options: ref.Options, Nullable: jc.Nullable || (e.Parent != nil && e.Inheritance == SingleTable), Assoc: a, GoType: ref.GoType}
			if existing := e.FieldByColumn(jc.Name); existing != nil {
				return errf("join column %q collides with field %s", jc.Name, existing.Name)
			}
			e.Fields = append(e.Fields, fk)
			if a.Kind == OneToOne {
				e.Indexes = append(e.Indexes, &IndexDef{Name: schema.IdentifierName("uniq", e.Table, []string{jc.Name}), Columns: []string{jc.Name}, Unique: true})
			}
		}
		if a.JoinTable != nil {
			srcID, tgtID := e.SingleID(), target.SingleID()
			if srcID == nil || tgtID == nil {
				return errf("manyToMany requires single column identifiers on both sides")
			}
			if a.JoinTable.Name == "" {
				a.JoinTable.Name = r.naming.JoinTableName(e.Table, target.Table)
			}
			src := r.naming.JoinColumnName(e.Name, srcID.Column)
			tgt := r.naming.JoinColumnName(target.Name, tgtID.Column)
			if src == tgt { // self referencing
				tgt = r.naming.JoinColumnName("related_"+target.Name, tgtID.Column)
			}
			a.JoinTable.JoinColumns = []JoinColumn{{Name: src, Referenced: srcID.Column, OnDelete: "CASCADE"}}
			a.JoinTable.InverseJoinColumns = []JoinColumn{{Name: tgt, Referenced: tgtID.Column, OnDelete: "CASCADE"}}
		}
	}
	if err := e.entityIndexes(e.indexSpec); err != nil {
		return &MappingError{Entity: e.Name, Msg: err.Error()}
	}
	e.index()
	// Validate bidirectional pairs once both sides are known.
	for _, a := range e.Assocs {
		if a.Inherited {
			continue
		}
		other := ""
		if a.MappedBy != "" {
			other = a.MappedBy
		} else if a.InversedBy != "" {
			other = a.InversedBy
		} else {
			continue
		}
		inv := a.Target.Assoc(other)
		errf := func(format string, args ...any) error {
			return &MappingError{Entity: e.Name, Field: a.Name, Msg: fmt.Sprintf(format, args...)}
		}
		if inv == nil {
			return errf("%s.%s does not exist", a.Target.Name, other)
		}
		if inv.TargetType != e.GoType {
			return errf("%s.%s points to %s, not %s", a.Target.Name, other, inv.TargetType.Name(), e.Name)
		}
		if !compatibleKinds(a.Kind, inv.Kind) {
			return errf("%s is incompatible with %s on %s.%s", a.Kind, inv.Kind, a.Target.Name, other)
		}
		if a.MappedBy != "" && !inv.Owning {
			return errf("mappedBy must name the owning side, but %s.%s is inverse", a.Target.Name, other)
		}
	}
	return nil
}

func compatibleKinds(a, b AssocKind) bool {
	switch a {
	case ManyToOne:
		return b == OneToMany
	case OneToMany:
		return b == ManyToOne
	}
	return a == b
}
