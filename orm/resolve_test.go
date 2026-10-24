package orm

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/entity"
)

// sdSite is soft deletable; sdLog refers to it.
type sdSite struct {
	entity.Header `smorm:"table:sd_site"`
	ID            int64
	Title         string
	DeletedAt     *time.Time `smorm:"column:deleted_at"`
}

func (s *sdSite) MarkDeleted(now time.Time) { s.DeletedAt = &now }
func (s *sdSite) IsDeleted() bool           { return s.DeletedAt != nil }
func (s *sdSite) DeletedColumn() string     { return "deleted_at" }

type sdLog struct {
	entity.Header `smorm:"table:sd_log"`
	ID            int64
	Site          *sdSite
}

// TestReferencesToSoftDeleted: a reference points to a row that exists, so
// loading it ignores the soft delete filter; queries keep it.
func TestReferencesToSoftDeleted(t *testing.T) {
	em, rec := newUnitEM(t, dialect.Postgres)
	em.SetContext(context.Background())
	_ = em.reg.Register(sdLog{})
	rec.Returns("FROM", []string{"id", "title", "deleted_at"}, []driver.Value{int64(1), "gone", time.Now()})
	site, _ := em.Reference(typeOf[sdSite](), 1)
	entity.Touch(site)
	if site.(*sdSite).Title != "gone" {
		t.Fatal("reference to a soft-deleted row did not load")
	}
	if q := rec.Queries(); len(q) != 1 || strings.Contains(q[0], "deleted_at\" IS NULL") {
		t.Fatalf("lazy load filtered: %v", q)
	}
	if em.resolving {
		t.Fatal("resolving left on")
	}
	if s := sqlOf(t, em.NewQuery(typeOf[sdSite]())); !strings.Contains(s, `"deleted_at" IS NULL`) {
		t.Fatalf("queries must still hide deleted rows: %s", s)
	}
}
