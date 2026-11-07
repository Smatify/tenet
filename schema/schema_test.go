package schema

import (
	"strings"
	"testing"

	"github.com/smatify/tenet/types"
)

func buildBlog() *Schema {
	s := New()
	u := s.CreateTable("users")
	u.AddColumn("id", types.BigInt).SetAutoIncrement(true)
	u.AddColumn("email", types.String).SetLength(180)
	u.AddColumn("bio", types.Text).SetNullable(true).SetComment("about")
	u.AddColumn("score", types.Decimal).SetPrecision(10, 2).SetDefault("0")
	u.AddColumn("code", types.String).SetLength(2).SetFixed(true)
	u.SetPrimaryKey("id")
	u.AddUniqueIndex("email")
	p := s.CreateTable("posts")
	p.AddColumn("id", types.BigInt).SetAutoIncrement(true)
	p.AddColumn("author_id", types.BigInt)
	p.SetPrimaryKey("id")
	p.AddIndex("author_id").SetWhere("author_id > 0")
	p.AddForeignKey([]string{"author_id"}, "users", []string{"id"}).SetOnDelete(Cascade).SetOnUpdate(Restrict)
	s.CreateSequence("seq", 1, 1)
	return s
}

func TestBuildAndLookup(t *testing.T) {
	s := buildBlog()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	u := s.Table("USERS")
	if u == nil || !s.HasTable("posts") || s.Table("nope") != nil {
		t.Fatal("table lookup")
	}
	if c := u.Column("EMAIL"); c == nil || c.Options.Length != 180 || u.Column("x") != nil {
		t.Fatal("column lookup")
	}
	if u.PrimaryKey.Name != "pk_users" || !u.PrimaryKey.Primary {
		t.Fatalf("pk = %+v", u.PrimaryKey)
	}
	if u.Index("uniq_users_email") == nil || u.Index("x") != nil {
		t.Fatal("index lookup")
	}
	p := s.Table("posts")
	fk := p.ForeignKey("fk_posts_author_id")
	if fk == nil || fk.OnDelete != Cascade || fk.OnUpdate != Restrict || p.ForeignKey("x") != nil {
		t.Fatalf("fk = %+v", fk)
	}
	if p.Indexes[0].Where != "author_id > 0" {
		t.Fatal("partial index")
	}
	if s.Sequence("SEQ") == nil || s.Sequence("x") != nil {
		t.Fatal("sequence lookup")
	}
	if !u.Column("id").IsInteger() || u.Column("email").IsInteger() {
		t.Fatal("IsInteger")
	}
}

func TestCreateTableTwicePanics(t *testing.T) {
	s := New()
	s.CreateTable("a")
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	s.CreateTable("A")
}

func TestDrop(t *testing.T) {
	s := buildBlog()
	s.Table("users").DropColumn("BIO")
	if s.Table("users").Column("bio") != nil {
		t.Fatal("DropColumn")
	}
	s.DropTable("Posts")
	if s.HasTable("posts") || len(s.Tables) != 1 {
		t.Fatal("DropTable")
	}
}

func TestClone(t *testing.T) {
	s := buildBlog()
	c := s.Clone()
	c.Table("users").Column("email").SetLength(1)
	*c.Table("users").Column("score").Default = "5"
	c.Table("users").PrimaryKey.Columns[0] = "x"
	c.Table("posts").Indexes[0].Columns[0] = "x"
	c.Table("posts").ForeignKeys[0].RefColumns[0] = "x"
	c.Sequences[0].Start = 99
	u := s.Table("users")
	if u.Column("email").Options.Length != 180 || *u.Column("score").Default != "0" || u.PrimaryKey.Columns[0] != "id" ||
		s.Table("posts").Indexes[0].Columns[0] != "author_id" || s.Table("posts").ForeignKeys[0].RefColumns[0] != "id" ||
		s.Sequences[0].Start != 1 {
		t.Fatal("clone shares state")
	}
	noPK := &Table{Name: "x"}
	if noPK.Clone().PrimaryKey != nil {
		t.Fatal("nil pk")
	}
}

func TestValidate(t *testing.T) {
	s := New()
	empty := s.CreateTable("empty")
	_ = empty
	bad := s.CreateTable("bad")
	bad.AddColumn("id", types.String).SetAutoIncrement(true).SetNullable(true)
	bad.AddColumn("ID", types.BigInt).SetAutoIncrement(true)
	bad.SetPrimaryKey("id", "missing")
	bad.AddIndex()
	bad.AddIndex("id").SetName("dup")
	bad.AddIndex("id").SetName("DUP")
	bad.AddForeignKey([]string{"id"}, "nowhere", []string{"a", "b"})
	bad.AddForeignKey([]string{"id"}, "empty", []string{"nope"}).SetName("fk2")
	err := s.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		"empty: table has no columns", "duplicate column", "auto increment requires an integer", "more than one auto increment",
		`unknown column "missing"`, "must not be nullable", "has no columns", "duplicate index name", "references 2",
		`unknown table "nowhere"`, "unknown column empty.nope",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in\n%v", want, err)
		}
	}
}

func TestIdentifierName(t *testing.T) {
	if got := IdentifierName("idx", "User Profiles", []string{"First-Name", "last"}); got != "idx_user_profiles_first_name_last" {
		t.Fatalf("got %s", got)
	}
	long := IdentifierName("idx", strings.Repeat("t", 50), []string{strings.Repeat("c", 30)})
	if len(long) != MaxIdentifierLength {
		t.Fatalf("len = %d", len(long))
	}
	other := IdentifierName("idx", strings.Repeat("t", 50), []string{strings.Repeat("c", 31)})
	if long == other {
		t.Fatal("hash must disambiguate")
	}
	if IdentifierName("idx", strings.Repeat("t", 50), []string{strings.Repeat("c", 30)}) != long {
		t.Fatal("must be deterministic")
	}
}

func TestIndexSameColumns(t *testing.T) {
	a := &Index{Columns: []string{"a", "B"}}
	if !a.SameColumns(&Index{Columns: []string{"A", "b"}}) || a.SameColumns(&Index{Columns: []string{"b", "a"}}) {
		t.Fatal("SameColumns")
	}
}
