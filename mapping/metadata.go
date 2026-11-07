// Package mapping turns entity structs and their struct tags into metadata:
// tables, columns, identifiers, associations and indexes.
package mapping

import (
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/smatify/tenet/internal/entity"
	"github.com/smatify/tenet/types"
)

// HeaderType is the type every entity embeds (re-exported as tenet.Entity).
var HeaderType = reflect.TypeFor[entity.Header]()

// Generator is an identifier generation strategy.
type Generator string

// Identifier generators.
const (
	GenNone     Generator = "none"     // application assigns the ID
	GenIdentity Generator = "identity" // auto increment / identity column
	GenSequence Generator = "sequence" // database sequence
	GenUUIDv7   Generator = "uuid7"
	GenUUIDv4   Generator = "uuid4"
)

// Entity is the metadata of one entity type.
type Entity struct {
	GoType     reflect.Type // struct type (not pointer)
	Name       string       // Go type name
	Table      string
	Repository string // optional custom repository type name (for the maker)
	Comment    string
	Fields     []*Field // mapped columns in declaration order
	ID         []*Field
	Generator  Generator
	Sequence   string // sequence name for GenSequence
	Version    *Field
	Assocs     []*Association
	Indexes    []*IndexDef
	// DoctrineStyle is true when the entity uses unexported fields with
	// generated accessors (the default style of make:entity).
	DoctrineStyle bool
	HeaderIndex   []int // reflect index of the embedded header

	// Inheritance is set on every class of a hierarchy; Root is the class
	// that declares it (Root == e for the root and for classes without
	// inheritance). Subclasses embed their parent entity.
	Inheritance         Inheritance
	Root                *Entity
	Parent              *Entity
	ParentIndex         []int // reflect index of the embedded parent
	Subclasses          []*Entity
	DiscriminatorColumn string
	DiscriminatorValue  string
	Abstract            bool

	// Cacheable entities are stored in the second-level cache (tag option
	// cache, optional cacheTTL:5m); subclasses inherit it from the root.
	Cacheable bool
	CacheTTL  time.Duration

	// Generated reports whether a generated accessor is used (set by Accessor).
	Generated bool

	indexSpec    string // entity level index option, applied in resolve
	byName       map[string]*Field
	byColumn     map[string]*Field
	assocBy      map[string]*Association
	accessorOnce sync.Once
	accessor     Accessor
}

// Inheritance is the mapping strategy of a class hierarchy.
type Inheritance string

// Inheritance strategies.
const (
	SingleTable Inheritance = "single" // one table for the whole hierarchy
	Joined      Inheritance = "joined" // one table per class, joined by the identifier
)

// IsPolymorphic reports whether loading e may yield subclass instances.
func (e *Entity) IsPolymorphic() bool { return e.Inheritance != "" && len(e.Subclasses) > 0 }

// Descendants returns e and all its (transitive) subclasses.
func (e *Entity) Descendants() []*Entity {
	out := []*Entity{e}
	for i := 0; i < len(out); i++ {
		out = append(out, out[i].Subclasses...)
	}
	return out
}

// ClassForDiscriminator returns the class of the hierarchy with value v.
func (e *Entity) ClassForDiscriminator(v string) *Entity {
	for _, c := range e.Root.Descendants() {
		if c.DiscriminatorValue == v {
			return c
		}
	}
	return nil
}

// Tables returns the tables of e from the root down (one for single table
// inheritance and plain entities).
func (e *Entity) Tables() []string {
	if e.Inheritance != Joined {
		return []string{e.Table}
	}
	var chain []*Entity
	for c := e; c != nil; c = c.Parent {
		chain = append([]*Entity{c}, chain...)
	}
	out := make([]string, len(chain))
	for i, c := range chain {
		out[i] = c.Table
	}
	return out
}

// Field returns the field with the Go name, or nil.
func (e *Entity) Field(name string) *Field { return e.byName[name] }

// FieldByColumn returns the field mapped to column, or nil.
func (e *Entity) FieldByColumn(col string) *Field { return e.byColumn[strings.ToLower(col)] }

// Assoc returns the association with the Go name, or nil.
func (e *Entity) Assoc(name string) *Association { return e.assocBy[name] }

// SingleID returns the identifier field when the ID is not composite.
func (e *Entity) SingleID() *Field {
	if len(e.ID) == 1 {
		return e.ID[0]
	}
	return nil
}

// Columns returns the column names of all fields (including join columns of
// owning to-one associations, which are fields too).
func (e *Entity) Columns() []string {
	out := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		out[i] = f.Column
	}
	return out
}

func (e *Entity) index() {
	e.byName = map[string]*Field{}
	e.byColumn = map[string]*Field{}
	e.assocBy = map[string]*Association{}
	for i, f := range e.Fields {
		f.Pos = i
		if f.Name != "" {
			e.byName[f.Name] = f
		}
		e.byColumn[strings.ToLower(f.Column)] = f
	}
	for i, a := range e.Assocs {
		a.Pos = i
		e.assocBy[a.Name] = a
	}
}

// Field is a mapped column.
type Field struct {
	Table    string // table holding the column (differs per class with joined inheritance)
	Pos      int    // position in Entity.Fields (accessor index)
	Name     string // Go field name; empty for join columns without own field
	Column   string
	Index    []int        // reflect index path from the entity struct
	GoType   reflect.Type // field type
	Type     types.Type
	Options  types.Options
	Nullable bool
	NullZero bool // store the Go zero value as NULL
	Default  *string
	Comment  string
	ID       bool
	Version  bool
	Exported bool
	// Generated columns (generated:identity on a field other than the
	// identifier) get their value from the database when it is zero on
	// insert.
	Generated bool
	// Assoc is set for join columns of owning to-one associations.
	Assoc *Association

	generator Generator
}

// IsPointer reports whether the Go field is a pointer.
func (f *Field) IsPointer() bool { return f.GoType != nil && f.GoType.Kind() == reflect.Pointer }

// AssocKind is the cardinality of an association.
type AssocKind int

// Association kinds.
const (
	ManyToOne AssocKind = iota + 1
	OneToMany
	OneToOne
	ManyToMany
)

func (k AssocKind) String() string {
	return [...]string{"", "manyToOne", "oneToMany", "oneToOne", "manyToMany"}[k]
}

// IsToOne reports whether the association points to a single entity.
func (k AssocKind) IsToOne() bool { return k == ManyToOne || k == OneToOne }

// Container is how the Go field holds the associated entity or entities.
type Container int

// Containers.
const (
	ContainerPointer    Container = iota + 1 // *User (Doctrine style, proxy stubs)
	ContainerRef                             // tenet.Ref[User] (Go style)
	ContainerCollection                      // tenet.Collection[Post]
	ContainerSlice                           // []*Post (eager only)
)

// Fetch is the loading strategy of an association.
type Fetch string

// Fetch modes.
const (
	FetchLazy      Fetch = "lazy"
	FetchEager     Fetch = "eager"
	FetchExtraLazy Fetch = "extraLazy"
)

// Cascade is a set of operations propagated along an association.
type Cascade uint8

// Cascade operations.
const (
	CascadePersist Cascade = 1 << iota
	CascadeRemove
	CascadeRefresh
	CascadeDetach
	CascadeAll = CascadePersist | CascadeRemove | CascadeRefresh | CascadeDetach
)

// Has reports whether c includes op.
func (c Cascade) Has(op Cascade) bool { return c&op != 0 }

// JoinColumn maps a foreign key column to the referenced column.
type JoinColumn struct {
	Name       string
	Referenced string
	Nullable   bool
	OnDelete   string
}

// JoinTable describes the link table of a many-to-many association.
type JoinTable struct {
	Name               string
	JoinColumns        []JoinColumn // pointing to the owning entity
	InverseJoinColumns []JoinColumn // pointing to the target entity
}

// Association is a relation to another entity.
type Association struct {
	Pos           int // position in Entity.Assocs (accessor index)
	Name          string
	Kind          AssocKind
	Index         []int
	GoType        reflect.Type
	Container     Container
	TargetType    reflect.Type // struct type of the target entity
	Target        *Entity      // resolved target metadata
	Source        *Entity
	MappedBy      string // set on the inverse side
	InversedBy    string // set on the owning side of bidirectional associations
	Owning        bool
	JoinColumns   []JoinColumn
	JoinTable     *JoinTable
	Cascade       Cascade
	Fetch         Fetch
	OrphanRemoval bool
	OrderBy       string
	indexTag      Tag // the field options, for index and unique on the join column
	Exported      bool
	// Inherited is true for associations a subclass copied from its parent.
	Inherited bool
}

// Inverse returns the association on the other side, or nil when unidirectional.
func (a *Association) Inverse() *Association {
	if a.Target == nil {
		return nil
	}
	if a.MappedBy != "" {
		return a.Target.Assoc(a.MappedBy)
	}
	if a.InversedBy != "" {
		return a.Target.Assoc(a.InversedBy)
	}
	return nil
}

// IndexDef is an index declared through field tags.
type IndexDef struct {
	Name    string
	Columns []string
	Unique  bool
	Where   string // partial index predicate with column names ("" for none)
}

// CollectionField is implemented by collection container types such as
// tenet.Collection[T]; CollectionElem returns T.
type CollectionField interface{ CollectionElem() reflect.Type }

// RefField is implemented by single reference containers such as
// tenet.Ref[T]; RefElem returns T.
type RefField interface{ RefElem() reflect.Type }

// CreatedToucher is implemented by entities (usually through the Timestamps
// trait) that record their creation time. The unit of work calls it right
// before inserting.
type CreatedToucher interface{ TouchCreated(now time.Time) }

// UpdatedToucher is implemented by entities that record their last update.
// The unit of work calls it right before updating a changed entity.
type UpdatedToucher interface{ TouchUpdated(now time.Time) }
