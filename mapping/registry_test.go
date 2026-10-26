package mapping

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/types"
	"github.com/smatify/smorm/uuid"
)

type coll[T any] struct{ items []*T }

func (coll[T]) CollectionElem() reflect.Type { return reflect.TypeFor[T]() }

type ref[T any] struct{ p *T }

func (ref[T]) RefElem() reflect.Type { return reflect.TypeFor[T]() }

type Timestamps struct {
	CreatedAt time.Time
	UpdatedAt *time.Time
}

type User struct {
	entity.Header `smorm:"table:users,comment:'people'"`
	ID            int64
	Email         string  `smorm:"length:180,unique"`
	DisplayName   string  `smorm:"column:name,default:'anon'"`
	Bio           *string `smorm:"type:text"`
	Score         float64 `smorm:"nullzero"`
	Prefs         map[string]any
	Nick          sql.NullString
	Lock          int `smorm:"version"`
	Timestamps
	Posts   coll[Post]  `smorm:"oneToMany,mappedBy:author,cascade:persist|remove,orphanRemoval,orderBy:id"`
	Profile *Profile    `smorm:"oneToOne,mappedBy:User"`
	Groups  coll[Group] `smorm:"manyToMany,inversedBy:Members,cascade:all"`
	cache   string      `smorm:"-"`
}

type Post struct {
	entity.Header
	id     uuid.UUID `smorm:"id"`
	title  string    `smorm:"length:200,index:title_lang"`
	lang   string    `smorm:"length:2,fixed,index:title_lang"`
	slug   string    `smorm:"unique:uniq_slug"`
	author *User     `smorm:"manyToOne,inversedBy:Posts,notnull,onDelete:cascade"`
	editor ref[User] `smorm:"joinColumn:editor_user_id,fetch:eager"`
}

type Profile struct {
	entity.Header
	ID   int64
	User *User `smorm:"oneToOne,inversedBy:Profile"`
}

type Group struct {
	entity.Header `smorm:"sequence:group_ids"`
	ID            int32      `smorm:"generated:sequence"`
	Members       coll[User] `smorm:"manyToMany,mappedBy:Groups"`
}

func TestParseUser(t *testing.T) {
	r := NewRegistry()
	u, err := For[User](r)
	if err != nil {
		t.Fatal(err)
	}
	if u.Table != "users" || u.Comment != "people" || u.Name != "User" || u.DoctrineStyle {
		t.Fatalf("entity = %+v", u)
	}
	if id := u.SingleID(); id == nil || id.Name != "ID" || u.Generator != GenIdentity || id.Nullable {
		t.Fatalf("id = %+v gen=%s", id, u.Generator)
	}
	wantCols := []string{"id", "email", "name", "bio", "score", "prefs", "nick", "lock", "created_at", "updated_at"}
	if !slices.Equal(u.Columns(), wantCols) {
		t.Fatalf("columns = %v", u.Columns())
	}
	checks := []struct {
		field    string
		typ      types.Name
		nullable bool
	}{
		{"Email", types.String, false}, {"Bio", types.Text, true}, {"Score", types.Float, true},
		{"Prefs", types.JSON, false}, {"Nick", types.String, true}, {"CreatedAt", types.DateTime, false}, {"UpdatedAt", types.DateTime, true},
	}
	for _, c := range checks {
		f := u.Field(c.field)
		if f == nil || f.Type.Name() != c.typ || f.Nullable != c.nullable {
			t.Errorf("%s = %+v", c.field, f)
		}
	}
	if f := u.Field("UpdatedAt"); !f.IsPointer() || len(f.Index) != 2 {
		t.Errorf("embedded field index = %v", f.Index)
	}
	if u.Field("cache") != nil || u.FieldByColumn("NAME") == nil || u.Field("Score").NullZero != true {
		t.Fatal("field lookups")
	}
	if *u.Field("DisplayName").Default != "'anon'" || u.Field("Email").Options.Length != 180 {
		t.Fatal("options")
	}
	if u.Version == nil || u.Version.Name != "Lock" {
		t.Fatal("version")
	}
	if len(u.Indexes) != 1 || u.Indexes[0].Name != "uniq_users_email" || !u.Indexes[0].Unique {
		t.Fatalf("indexes = %+v", u.Indexes)
	}
	posts := u.Assoc("Posts")
	if posts.Kind != OneToMany || posts.Owning || posts.Container != ContainerCollection || !posts.Cascade.Has(CascadeRemove) ||
		posts.Cascade.Has(CascadeRefresh) || !posts.OrphanRemoval || posts.OrderBy != "id" || posts.Fetch != FetchLazy {
		t.Fatalf("posts = %+v", posts)
	}
	if inv := posts.Inverse(); inv == nil || inv.Name != "author" {
		t.Fatal("inverse of posts")
	}
	groups := u.Assoc("Groups")
	if !groups.Owning || groups.JoinTable == nil || groups.JoinTable.Name != "users_group" || groups.Cascade != CascadeAll ||
		groups.JoinTable.JoinColumns[0].Name != "user_id" || groups.JoinTable.InverseJoinColumns[0].Name != "group_id" {
		t.Fatalf("groups = %+v %+v", groups, groups.JoinTable)
	}
	if p := u.Assoc("Profile"); p.Owning || p.Kind != OneToOne || p.Container != ContainerPointer {
		t.Fatalf("profile = %+v", p)
	}
}

func TestParseDoctrineStylePost(t *testing.T) {
	r := NewRegistry()
	p, err := For[Post](r)
	if err != nil {
		t.Fatal(err)
	}
	if !p.DoctrineStyle || p.Table != "post" || p.Generator != GenUUIDv7 {
		t.Fatalf("post = %+v", p)
	}
	if !slices.Equal(p.Columns(), []string{"id", "title", "lang", "slug", "author_id", "editor_user_id"}) {
		t.Fatalf("columns = %v", p.Columns())
	}
	author := p.Assoc("author")
	if !author.Owning || author.InversedBy != "Posts" || author.Target.Name != "User" || author.Exported {
		t.Fatalf("author = %+v", author)
	}
	jc := author.JoinColumns[0]
	if jc.Name != "author_id" || jc.Referenced != "id" || jc.Nullable || jc.OnDelete != "CASCADE" {
		t.Fatalf("join column = %+v", jc)
	}
	fk := p.FieldByColumn("author_id")
	if fk.Assoc != author || fk.Type.Name() != types.BigInt || fk.Nullable || fk.Name != "" {
		t.Fatalf("fk field = %+v", fk)
	}
	editor := p.Assoc("editor")
	if editor.Kind != ManyToOne || editor.Container != ContainerRef || editor.Fetch != FetchEager || !editor.JoinColumns[0].Nullable {
		t.Fatalf("editor = %+v", editor)
	}
	if editor.Inverse() != nil {
		t.Fatal("editor is unidirectional")
	}
	idx := map[string]*IndexDef{}
	for _, d := range p.Indexes {
		idx[d.Name] = d
	}
	if d := idx["title_lang"]; d == nil || d.Unique || !slices.Equal(d.Columns, []string{"title", "lang"}) {
		t.Fatalf("title_lang = %+v", d)
	}
	if d := idx["uniq_slug"]; d == nil || !d.Unique {
		t.Fatalf("uniq_slug = %+v", d)
	}
}

func TestGroupSequenceAndRegistry(t *testing.T) {
	r := NewRegistry(WithNaming(SnakeCase{}), WithTypes(types.NewRegistry()))
	if err := r.Register(&Group{}, User{}); err != nil {
		t.Fatal(err)
	}
	g, _ := For[Group](r)
	if g.Generator != GenSequence || g.Sequence != "group_ids" {
		t.Fatalf("group = %+v", g)
	}
	names := []string{}
	for _, e := range r.All() {
		names = append(names, e.Name)
	}
	if !slices.Equal(names, []string{"Group", "Post", "Profile", "User"}) {
		t.Fatalf("All = %v", names)
	}
	if r.Types() == nil || r.Naming() == nil || Default == nil {
		t.Fatal("accessors")
	}
	if !IsEntity(reflect.TypeFor[*User]()) || IsEntity(reflect.TypeFor[int]()) || IsEntity(reflect.TypeFor[Timestamps]()) {
		t.Fatal("IsEntity")
	}
	if ManyToMany.String() != "manyToMany" || !OneToOne.IsToOne() || OneToMany.IsToOne() {
		t.Fatal("AssocKind")
	}
}

func TestBuildSchema(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(User{}); err != nil {
		t.Fatal(err)
	}
	s := BuildSchema(r.All())
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	var tables []string
	for _, t := range s.Tables {
		tables = append(tables, t.Name)
	}
	if !slices.Equal(tables, []string{"group", "post", "profile", "users", "users_group"}) {
		t.Fatalf("tables = %v", tables)
	}
	users := s.Table("users")
	if !users.Column("id").AutoIncrement || users.Comment != "people" || users.Column("bio").Type != types.Text {
		t.Fatal("users columns")
	}
	post := s.Table("post")
	if post.Column("id").AutoIncrement || post.ForeignKey("fk_post_author_id").OnDelete != schema.Cascade {
		t.Fatalf("post = %+v", post)
	}
	if post.Index("idx_post_author_id") == nil || post.Index("idx_post_editor_user_id") == nil {
		t.Fatal("join columns must be indexed")
	}
	profile := s.Table("profile")
	if profile.Index("uniq_profile_user_id") == nil || profile.Index("idx_profile_user_id") != nil {
		t.Fatal("oneToOne join column gets a unique index only")
	}
	jt := s.Table("users_group")
	if !slices.Equal(jt.PrimaryKey.Columns, []string{"user_id", "group_id"}) || len(jt.ForeignKeys) != 2 ||
		jt.Column("group_id").Type != types.Integer || jt.Index("idx_users_group_group_id") == nil {
		t.Fatalf("join table = %+v", jt)
	}
	if s.Sequence("group_ids") == nil || s.Table("group").Column("id").AutoIncrement {
		t.Fatal("sequence generator")
	}
}

// ---- invalid mappings

type NoHeader struct{ ID int }
type NoID struct {
	entity.Header
	Name string
}
type BadEntityOpt struct {
	entity.Header `smorm:"tablename:x"`
	ID            int
}
type BadFieldOpt struct {
	entity.Header
	ID int `smorm:"colum:x"`
}
type BadTagSyntax struct {
	entity.Header
	ID int `smorm:"default:'x"`
}
type BadEntityTag struct {
	entity.Header `smorm:"comment:'x"`
	ID            int
}
type UnknownType struct {
	entity.Header
	ID int `smorm:"type:nope"`
}
type NoInfer struct {
	entity.Header
	ID int
	X  struct{ A int } `smorm:"column:x"`
}
type BadLength struct {
	entity.Header
	ID int
	S  string `smorm:"length:-1"`
}
type BadPrecision struct {
	entity.Header
	ID int
	S  float64 `smorm:"precision:x"`
}
type BadScale struct {
	entity.Header
	ID int
	S  float64 `smorm:"scale:x"`
}
type NullConflict struct {
	entity.Header
	ID int
	S  string `smorm:"nullable,notnull"`
}
type TwoIdentities struct {
	entity.Header
	ID     int
	Number int64 `smorm:"generated:identity"`
}

type GenOnNonID struct {
	entity.Header
	ID int
	S  string `smorm:"generated:uuid7"`
}
type BadGen struct {
	entity.Header
	ID int `smorm:"generated:magic"`
}
type TwoVersions struct {
	entity.Header
	ID int
	A  int `smorm:"version"`
	B  int `smorm:"version"`
}
type MixedIndex struct {
	entity.Header
	ID int
	A  int `smorm:"index:g"`
	B  int `smorm:"unique:g"`
}
type DupColumn struct {
	entity.Header
	ID int
	A  int `smorm:"column:id"`
}
type BadContainer struct {
	entity.Header
	ID int
	A  map[string]*User `smorm:"manyToOne"`
}
type CollNoKind struct {
	entity.Header
	ID    int
	Users coll[User]
}
type ToOneInSlice struct {
	entity.Header
	ID    int
	Users []*User `smorm:"manyToOne"`
}
type BothSides struct {
	entity.Header
	ID int
	U  *User `smorm:"oneToOne,mappedBy:a,inversedBy:b"`
}
type ManyToOneMapped struct {
	entity.Header
	ID int
	U  *User `smorm:"manyToOne,mappedBy:Posts"`
}
type OneToManyNoMapped struct {
	entity.Header
	ID    int
	Users coll[User] `smorm:"oneToMany"`
}
type BadCascade struct {
	entity.Header
	ID int
	U  *User `smorm:"cascade:explode"`
}
type BadFetch struct {
	entity.Header
	ID int
	U  *User `smorm:"fetch:sometimes"`
}
type BadOrphan struct {
	entity.Header
	ID int
	U  *User `smorm:"orphanRemoval"`
}
type InverseJoin struct {
	entity.Header
	ID int
	U  *User `smorm:"oneToOne,mappedBy:Profile,joinColumn:x"`
}
type TargetNotEntity struct {
	entity.Header
	ID int
	T  *Timestamps `smorm:"manyToOne"`
}
type MissingInverse struct {
	entity.Header
	ID int
	U  *User `smorm:"manyToOne,inversedBy:Nope"`
}
type WrongInverseTarget struct {
	entity.Header
	ID int
	U  *User `smorm:"manyToOne,inversedBy:Posts"`
}
type BadRefColumn struct {
	entity.Header
	ID int
	U  *User `smorm:"referencedColumn:nope"`
}
type JoinCollision struct {
	entity.Header
	ID     int
	UserID int
	U      *User `smorm:"joinColumn:user_id"`
}
type CompositeTarget struct {
	entity.Header
	A int `smorm:"id"`
	B int `smorm:"id"`
}
type RefComposite struct {
	entity.Header
	ID int
	C  *CompositeTarget
}
type M2MComposite struct {
	entity.Header
	ID int
	C  coll[CompositeTarget] `smorm:"manyToMany"`
}
type IncompatInverse struct {
	entity.Header
	ID int
	B  *IncB `smorm:"oneToOne,mappedBy:A"`
}
type IncB struct {
	entity.Header
	ID int
	A  *IncompatInverse `smorm:"manyToOne"`
}
type InverseOfInverse struct {
	entity.Header
	ID int
	B  *InvB `smorm:"oneToOne,mappedBy:A"`
}
type InvB struct {
	entity.Header
	ID int
	A  *InverseOfInverse `smorm:"oneToOne,mappedBy:B"`
}

func TestInvalidMappings(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{NoHeader{}, "not an entity"},
		{NoID{}, "no identifier"},
		{BadEntityOpt{}, `unknown entity tag option "tablename"`},
		{BadFieldOpt{}, `unknown tag option "colum"`},
		{BadTagSyntax{}, "unterminated"},
		{BadEntityTag{}, "unterminated"},
		{UnknownType{}, `unknown type "nope"`},
		{NoInfer{}, "cannot infer"},
		{BadLength{}, "length must"},
		{BadPrecision{}, "precision must"},
		{BadScale{}, "scale must"},
		{NullConflict{}, "mutually exclusive"},
		{GenOnNonID{}, "can only be generated:identity integers"},
		{TwoIdentities{}, "at most one identity column"},
		{BadGen{}, "unknown generator"},
		{TwoVersions{}, "already has version"},
		{MixedIndex{}, "both unique and non-unique"},
		{DupColumn{}, "already mapped"},
		{BadContainer{}, "unsupported association type"},
		{CollNoKind{}, "explicit oneToMany"},
		{ToOneInSlice{}, "cannot be held"},
		{BothSides{}, "mutually exclusive"},
		{ManyToOneMapped{}, "always the owning side"},
		{OneToManyNoMapped{}, "requires mappedBy"},
		{BadCascade{}, "unknown cascade"},
		{BadFetch{}, "unknown fetch"},
		{BadOrphan{}, "orphanRemoval is only valid"},
		{InverseJoin{}, "owning side"},
		{TargetNotEntity{}, "not an entity"},
		{MissingInverse{}, "does not exist"},
		{WrongInverseTarget{}, "points to Post"},
		{BadRefColumn{}, "referenced column"},
		{JoinCollision{}, "collides"},
		{RefComposite{}, "composite identifier"},
		{M2MComposite{}, "single column identifiers"},
		{IncompatInverse{}, "incompatible"},
		{InverseOfInverse{}, "must name the owning side"},
	}
	for _, c := range cases {
		v, want := c.v, c.want
		r := NewRegistry()
		err := r.Register(v)
		var me *MappingError
		if err == nil || !errors.As(err, &me) || !strings.Contains(err.Error(), want) {
			t.Errorf("%T: got %v, want %q", v, err, want)
		}
	}
	if _, err := NewRegistry().Get(reflect.TypeFor[int]()); err == nil {
		t.Fatal("non-struct")
	}
	if (&MappingError{Entity: "E", Msg: "m"}).Error() != "smorm: E: m" {
		t.Fatal("MappingError format")
	}
}

type SelfRef struct {
	entity.Header
	ID      int64
	Parent  *SelfRef      `smorm:"inversedBy:Children"`
	Kids    coll[SelfRef] `smorm:"oneToMany,mappedBy:Parent"`
	Friends coll[SelfRef] `smorm:"manyToMany"`
}

func TestSelfReference(t *testing.T) {
	r := NewRegistry()
	_, err := For[SelfRef](r)
	if err == nil || !strings.Contains(err.Error(), "Children") {
		t.Fatalf("expected inverse mismatch error, got %v", err)
	}
	if len(r.All()) != 0 {
		t.Fatal("failed entity must not stay cached")
	}
}

type Node struct {
	entity.Header
	ID       int64
	Parent   *Node      `smorm:"inversedBy:Children"`
	Children coll[Node] `smorm:"oneToMany,mappedBy:Parent"`
	Friends  coll[Node] `smorm:"manyToMany"`
}

func TestSelfReferenceValid(t *testing.T) {
	r := NewRegistry()
	n, err := For[Node](r)
	if err != nil {
		t.Fatal(err)
	}
	jt := n.Assoc("Friends").JoinTable
	if jt.Name != "node_node" || jt.JoinColumns[0].Name != "node_id" || jt.InverseJoinColumns[0].Name != "related_node_id" {
		t.Fatalf("self m2m = %+v", jt)
	}
	if err := BuildSchema(r.All()).Validate(); err != nil {
		t.Fatal(err)
	}
}

type IndexedPost struct {
	entity.Header `smorm:"index:author|CreatedAt;editor"`
	ID            int64
	Timestamps
	author *User     `smorm:"manyToOne"`
	editor ref[User] `smorm:"joinColumn:editor_user_id"`
}

func TestEntityIndexes(t *testing.T) {
	e, err := For[IndexedPost](NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range e.Indexes {
		got = append(got, d.Name+"="+strings.Join(d.Columns, ","))
	}
	want := []string{"idx_indexed_post_author_id_created_at=author_id,created_at", "idx_indexed_post_editor_user_id=editor_user_id"}
	if !slices.Equal(got, want) {
		t.Fatalf("indexes = %v", got)
	}
	type bad struct {
		entity.Header `smorm:"index:missing"`
		ID            int64
	}
	if _, err := For[bad](NewRegistry()); err == nil || !strings.Contains(err.Error(), `unknown field "missing"`) {
		t.Fatalf("err = %v", err)
	}
}

type PendingPost struct {
	entity.Header `smorm:"index:author|CreatedAt?{approvedAt} IS NULL AND {author} IS NOT NULL"`
	ID            int64
	Timestamps
	author     *User `smorm:"manyToOne"`
	approvedAt *time.Time
}

func TestPartialEntityIndex(t *testing.T) {
	e, err := For[PendingPost](NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Indexes) != 1 {
		t.Fatalf("indexes = %+v", e.Indexes)
	}
	d := e.Indexes[0]
	if d.Name != "pidx_pending_post_author_id_created_at" || d.Where != "approved_at IS NULL AND author_id IS NOT NULL" {
		t.Fatalf("index = %+v", d)
	}
	table := BuildSchema([]*Entity{e}).Table("pending_post")
	if ddl := table.Index(d.Name); ddl == nil || ddl.Where != d.Where {
		t.Fatalf("schema index = %+v", ddl)
	}
	// The partial index holds some rows only: the foreign key keeps its own.
	if table.Index("idx_pending_post_author_id") == nil {
		t.Fatalf("indexes = %+v", table.Indexes)
	}
	type bad struct {
		entity.Header `smorm:"index:CreatedAt?{missing} IS NULL"`
		ID            int64
		Timestamps
	}
	if _, err := For[bad](NewRegistry()); err == nil || !strings.Contains(err.Error(), `unknown field "missing" in predicate`) {
		t.Fatalf("err = %v", err)
	}
}

type NumberedPost struct {
	entity.Header
	ID     uuid.UUID `smorm:"id"`
	Number int64     `smorm:"generated:identity"`
}

func TestGeneratedField(t *testing.T) {
	e, err := NewRegistry().Get(reflect.TypeFor[NumberedPost]())
	if err != nil {
		t.Fatal(err)
	}
	f := e.Field("Number")
	if !f.Generated || f.Nullable || e.Generator != GenUUIDv7 {
		t.Fatalf("field = %+v, generator %s", f, e.Generator)
	}
	if len(e.Indexes) != 1 || !e.Indexes[0].Unique || e.Indexes[0].Columns[0] != "number" {
		t.Fatalf("indexes = %+v", e.Indexes)
	}
	if c := BuildSchema([]*Entity{e}).Table("numbered_post").Column("number"); !c.AutoIncrement {
		t.Fatal("column must be auto increment")
	}
}

type IndexedComment struct {
	entity.Header `smorm:"table:indexed_comment"`
	ID            int64
	module        string `smorm:"index:idx_place"`
	author        *User  `smorm:"manyToOne,index"`
	post          *User  `smorm:"manyToOne,index:idx_place"`
	owner         *User  `smorm:"manyToOne,unique"`
}

func TestAssociationIndexes(t *testing.T) {
	e, err := For[IndexedComment](NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range e.Indexes {
		got = append(got, fmt.Sprintf("%s=%s/%v", d.Name, strings.Join(d.Columns, ","), d.Unique))
	}
	want := []string{"idx_place=module,post_id/false", "idx_indexed_comment_author_id=author_id/false", "uniq_indexed_comment_owner_id=owner_id/true"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("indexes = %v", got)
	}
	type bad struct {
		entity.Header
		ID      int64
		authors []*User `smorm:"oneToMany,mappedBy:x,index"`
	}
	if _, err := For[bad](NewRegistry()); err == nil || !strings.Contains(err.Error(), "only valid on the owning side") {
		t.Fatalf("err = %v", err)
	}
}
