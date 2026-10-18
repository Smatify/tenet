package validate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/entity"
	"github.com/smatify/smorm/internal/testdialect"
	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/uuid"
)

type vAuthor struct {
	entity.Header `smorm:"table:v_author"`
	ID            int64
	Name          string   `smorm:"index:idx_name"`
	Posts         []*vPost `smorm:"oneToMany,mappedBy:Author,orderBy:Title DESC,orphanRemoval"`
}

type vPost struct {
	entity.Header `smorm:"table:v_post"`
	ID            int64
	Title         string
	Author        *vAuthor `smorm:"inversedBy:Posts"`
}

type vDup struct {
	entity.Header `smorm:"table:v_author"`
	ID            int64
	Code          string `smorm:"index:idx_name"`
}

type vBadVersion struct {
	entity.Header
	ID  int64
	Ver string `smorm:"version"`
}

type vStringIdentity struct {
	entity.Header
	ID string `smorm:"generated:identity"`
}

type vIntUUID struct {
	entity.Header
	ID int64 `smorm:"generated:uuid7"`
}

type vSeq struct {
	entity.Header
	ID int64 `smorm:"generated:sequence"`
}

type vBadOrder struct {
	entity.Header
	ID    int64
	Posts []*vOrderPost `smorm:"oneToMany,mappedBy:Owner,orderBy:nope"`
}

type vOrderPost struct {
	entity.Header
	ID    int64
	Owner *vBadOrder `smorm:"inversedBy:Posts,cascade:remove"`
}

type vOrphanCascade struct {
	entity.Header
	ID   int64
	Kids []*vKid `smorm:"oneToMany,mappedBy:Parent,orphanRemoval"`
}

type vKid struct {
	entity.Header
	ID     int64
	Parent *vOrphanCascade `smorm:"inversedBy:Kids,cascade:remove"`
}

type vOK struct {
	entity.Header
	ID  uuid.UUID
	Ver time.Time `smorm:"version"`
}

func load(t *testing.T, vs ...any) []*mapping.Entity {
	t.Helper()
	r := mapping.NewRegistry()
	if err := r.Register(vs...); err != nil {
		t.Fatal(err)
	}
	return r.All()
}

func TestCheckMappingOK(t *testing.T) {
	if errs := CheckMapping(load(t, vAuthor{}, vOK{}), testdialect.Like(dialect.Postgres)); len(errs) != 0 {
		t.Fatalf("unexpected: %v", errs)
	}
}

func TestCheckMappingProblems(t *testing.T) {
	cases := []struct {
		entities []any
		d        dialect.Dialect
		want     string
	}{
		{[]any{vAuthor{}, vDup{}}, nil, `both mapped to table "v_author"`},
		{[]any{vBadVersion{}}, nil, "version fields must be"},
		{[]any{vStringIdentity{}}, nil, "identity generation requires"},
		{[]any{vIntUUID{}}, nil, "UUID generation requires"},
		{[]any{vSeq{}}, noSeq(), "does not support sequences"},
		{[]any{vBadOrder{}}, nil, `orderBy references unknown field "nope"`},
		{[]any{vOrphanCascade{}}, nil, "deletes the parent"},
	}
	for _, c := range cases {
		errs := CheckMapping(load(t, c.entities...), c.d)
		if !strings.Contains(strings.Join(errs, "\n"), c.want) {
			t.Errorf("%T: got %v, want %q", c.entities[0], errs, c.want)
		}
	}
	// index names shared by two different tables
	r := mapping.NewRegistry()
	_ = r.Register(vAuthor{})
	a, _ := mapping.For[vAuthor](r)
	s, _ := mapping.For[vSeq](r)
	s.Indexes = append(s.Indexes, &mapping.IndexDef{Name: "idx_name", Columns: []string{"id"}})
	if errs := CheckMapping([]*mapping.Entity{a, s}, nil); !strings.Contains(strings.Join(errs, "\n"), "unique per schema") {
		t.Fatalf("got %v", errs)
	}
	// target outside the validated set
	if errs := CheckMapping([]*mapping.Entity{a}, nil); !strings.Contains(strings.Join(errs, "\n"), "not part of the validated set") {
		t.Fatalf("got %v", errs)
	}
}

func TestRunWithoutDatabase(t *testing.T) {
	r, err := Run(context.Background(), Options{Entities: load(t, vAuthor{})})
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || !r.DatabaseOK() || !strings.Contains(r.String(), "[SKIPPED]") || !strings.Contains(r.String(), "[OK] The mapping") {
		t.Fatalf("report:\n%s", r)
	}
}

func TestReportString(t *testing.T) {
	r := &Report{Mapping: []string{"bad"}, Generated: []string{"stale"}, Diff: &schema.Diff{DropTables: []*schema.Table{{Name: "x"}}}}
	s := r.String()
	for _, want := range []string{"[ERROR] bad", "[ERROR] stale", "not in sync", "drop table x"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if r.OK() || r.MappingOK() || r.DatabaseOK() {
		t.Fatal("must not be OK")
	}
	r = &Report{Diff: &schema.Diff{}}
	if !strings.Contains(r.String(), "in sync") || !r.OK() {
		t.Fatal("in sync")
	}
}

func noSeq() dialect.Dialect {
	d := testdialect.Like(dialect.MySQL)
	d.F &^= dialect.FeatureSequences
	return d
}
