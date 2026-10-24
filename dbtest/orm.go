package dbtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smatify/smorm"
	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/mapping"
	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/uuid"
)

func init() {
	Register(
		Case{"ORM/PersistFindUpdateRemove", testORMCrud},
		Case{"ORM/References", testORMReferences},
		Case{"ORM/CommitOrderAndCycles", testORMCycles},
		Case{"ORM/RefreshDetachClear", testORMRefreshDetach},
		Case{"ORM/FailedFlushAndTransactions", testORMFailures},
		Case{"ORM/Generators", testORMGenerators},
	)
}

type ormAuthor struct {
	smorm.Entity `smorm:"table:orm_author"`
	smorm.IntID
	smorm.Timestamps
	name  string         `smorm:"length:100,unique"`
	email *string        `smorm:"length:180"`
	score int            `smorm:"nullzero"`
	prefs map[string]any `smorm:"nullable"`
}

type ormBook struct {
	smorm.Entity `smorm:"table:orm_book"`
	smorm.UUIDID
	title  string     `smorm:"length:200"`
	author *ormAuthor `smorm:"manyToOne,notnull,onDelete:cascade"`
	editor *ormAuthor `smorm:"manyToOne,cascade:persist"`
}

type ormCategory struct {
	smorm.Entity `smorm:"table:orm_category"`
	ID           int64
	Name         string       `smorm:"length:50"`
	Parent       *ormCategory `smorm:"manyToOne"`
}

type ormPerson struct {
	smorm.Entity `smorm:"table:orm_person"`
	ID           int64
	Name         string     `smorm:"length:50"`
	Friend       *ormPerson `smorm:"manyToOne"`
}

type ormTicket struct {
	smorm.Entity `smorm:"table:orm_ticket"`
	ID           int64  `smorm:"generated:sequence"`
	Subject      string `smorm:"length:50"`
}

type ormLine struct {
	smorm.Entity `smorm:"table:orm_line"`
	OrderNo      int64 `smorm:"id"`
	LineNo       int32 `smorm:"id"`
	Qty          int32
}

type ormCounter struct {
	smorm.Entity `smorm:"table:orm_counter"`
	ID           int64
}

var ormRegistry = sync.OnceValues(func() (*mapping.Registry, error) {
	r := mapping.NewRegistry()
	return r, r.Register(ormAuthor{}, ormBook{}, ormCategory{}, ormPerson{}, ormLine{}, ormCounter{})
})

// setupORM creates the ORM test tables and returns a fresh entity manager factory.
func setupORM(t *testing.T, db *dbal.DB, extra ...any) func() *smorm.EntityManager {
	t.Helper()
	base, err := ormRegistry()
	if err != nil {
		t.Fatal(err)
	}
	reg := base
	if len(extra) > 0 {
		reg = mapping.NewRegistry()
		if err := reg.Register(append([]any{ormAuthor{}, ormBook{}, ormCategory{}, ormPerson{}, ormLine{}, ormCounter{}}, extra...)...); err != nil {
			t.Fatal(err)
		}
	}
	s := mapping.BuildSchema(reg.All())
	DropSchemaQuietly(db, s)
	t.Cleanup(func() { DropSchemaQuietly(db, s) })
	stmts, err := schema.NewDDL(db.Dialect()).CreateSchema(s)
	if err != nil {
		t.Fatal(err)
	}
	ApplySchema(t, db, stmts)
	return func() *smorm.EntityManager { return smorm.NewEntityManager(db, smorm.WithRegistry(reg)) }
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func testORMCrud(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()

	email := "ann@example.com"
	ann := &ormAuthor{name: "ann", email: &email, prefs: map[string]any{"lang": "de"}}
	book := &ormBook{title: "Go", author: ann}
	must(t, em.Persist(ann))
	must(t, em.Persist(book))
	if book.ID().IsZero() {
		t.Fatal("UUID must be assigned on persist")
	}
	must(t, em.Flush(ctx))
	if ann.ID() == 0 || ann.CreatedAt().IsZero() || ann.UpdatedAt() != ann.CreatedAt() {
		t.Fatalf("after insert: id=%d created=%v", ann.ID(), ann.CreatedAt())
	}
	if !em.Contains(ann) || em.State(ann) != 1 {
		t.Fatal("ann must be managed")
	}
	// Identity map: same pointer without a query.
	same, err := smorm.Find[ormAuthor](ctx, em, ann.ID())
	must(t, err)
	if same != ann {
		t.Fatal("identity map must return the same instance")
	}
	// A second entity manager loads from the database.
	em2 := newEM()
	loaded, err := smorm.Find[ormAuthor](ctx, em2, ann.ID())
	must(t, err)
	if loaded == ann || loaded.name != "ann" || *loaded.email != email || loaded.prefs["lang"] != "de" || loaded.score != 0 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if !loaded.CreatedAt().Equal(ann.CreatedAt().Truncate(time.Microsecond)) && loaded.CreatedAt().Sub(ann.CreatedAt()).Abs() > time.Second {
		t.Fatalf("created_at %v vs %v", loaded.CreatedAt(), ann.CreatedAt())
	}
	lb, err := smorm.Find[ormBook](ctx, em2, book.ID())
	must(t, err)
	if lb.author != loaded || lb.editor != nil {
		t.Fatal("manyToOne must resolve through the identity map")
	}
	// Freshly hydrated entities (NULL editor, JSON, timestamps) are unchanged.
	checking, writes := true, 0
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if checking && ev.Operation != "SELECT" {
			writes++
		}
	}})
	must(t, em2.Flush(ctx))
	if checking = false; writes != 0 {
		t.Fatalf("flush of loaded entities wrote %d statements", writes)
	}
	// Update: only changed columns, updated_at advances.
	var updates []string
	db.AddQueryHook(dbal.QueryHookFuncs{After: func(_ context.Context, ev *dbal.QueryEvent) {
		if ev.Operation == "UPDATE" && strings.Contains(ev.Query, "orm_author") {
			updates = append(updates, ev.Query)
		}
	}})
	before := loaded.UpdatedAt()
	time.Sleep(5 * time.Millisecond)
	loaded.name = "annie"
	loaded.score = 7
	must(t, em2.Flush(ctx))
	if len(updates) != 1 || strings.Contains(updates[0], "email") || !strings.Contains(updates[0], "updated_at") {
		t.Fatalf("updates = %v", updates)
	}
	if !loaded.UpdatedAt().After(before) {
		t.Fatal("updated_at must advance")
	}
	must(t, em2.Flush(ctx)) // nothing changed: no statement
	if len(updates) != 1 {
		t.Fatalf("unchanged flush issued %v", updates)
	}
	// JSON in-place mutation is detected.
	loaded.prefs["lang"] = "en"
	must(t, em2.Flush(ctx))
	if len(updates) != 2 {
		t.Fatalf("JSON change not detected: %v", updates)
	}
	em3 := newEM()
	again, err := smorm.Find[ormAuthor](ctx, em3, ann.ID())
	must(t, err)
	if again.name != "annie" || again.score != 7 || again.prefs["lang"] != "en" {
		t.Fatalf("again = %+v", again)
	}
	// nullzero writes NULL for zero values.
	again.score = 0
	must(t, em3.Flush(ctx))
	var isNull bool
	must(t, db.NewSelect().ColumnExpr("CASE WHEN score IS NULL THEN 1 ELSE 0 END").Table("orm_author").Where("id = ?", ann.ID()).Scan(ctx, &isNull))
	if !isNull {
		t.Fatal("nullzero must store NULL")
	}
	// Remove cascades in the database (ON DELETE CASCADE) and in the ORM.
	must(t, em3.Remove(again))
	must(t, em3.Flush(ctx))
	if _, err := smorm.Find[ormAuthor](ctx, em3, ann.ID()); !errors.Is(err, smorm.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := smorm.Find[ormBook](ctx, newEM(), book.ID()); !errors.Is(err, smorm.ErrNotFound) {
		t.Fatalf("book err = %v", err)
	}
}

func testORMReferences(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	ann := &ormAuthor{name: "ann"}
	must(t, em.Persist(ann))
	// Cascade persist through editor; author must be persisted explicitly.
	ed := &ormAuthor{name: "ed"}
	b := &ormBook{title: "T", author: ann, editor: ed}
	must(t, em.Persist(b))
	if !em.Contains(ed) {
		t.Fatal("cascade persist")
	}
	must(t, em.Flush(ctx))

	unsaved := &ormAuthor{name: "ghost"}
	b.author = unsaved
	var nf interface{ Error() string }
	err := em.Flush(ctx)
	if err == nil || !strings.Contains(err.Error(), "cascade persist") {
		t.Fatalf("expected NewEntityFoundError, got %v", err)
	}
	_ = nf

	em2 := newEM()
	ref, err := smorm.Reference[ormAuthor](em2, ann.ID())
	must(t, err)
	if em2.IsInitialized(ref) || ref.ID() != ann.ID() || ref.name != "" {
		t.Fatal("reference must be an uninitialized stub")
	}
	lb, err := smorm.Find[ormBook](ctx, em2, b.ID())
	must(t, err)
	if lb.author != ref {
		t.Fatal("hydration must reuse the reference")
	}
	must(t, em2.Initialize(ctx, ref))
	if !em2.IsInitialized(ref) || ref.name != "ann" {
		t.Fatal("Initialize must load the reference")
	}
	// Setting an association to a reference writes only the FK.
	edRef, _ := smorm.Reference[ormAuthor](em2, ed.ID())
	lb.author = edRef
	must(t, em2.Flush(ctx))
	check, err := smorm.Find[ormBook](ctx, newEM(), b.ID())
	must(t, err)
	if check.author.ID() != ed.ID() {
		t.Fatal("FK update via reference")
	}
	if _, err := smorm.Reference[ormAuthor](em2); err == nil {
		t.Fatal("wrong identifier count")
	}
}

func testORMCycles(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	root := &ormCategory{Name: "root"}
	child := &ormCategory{Name: "child", Parent: root}
	leaf := &ormCategory{Name: "leaf", Parent: child}
	// Persist children first; the commit order must insert parents first.
	for _, c := range []*ormCategory{leaf, child, root} {
		must(t, em.Persist(c))
	}
	must(t, em.Flush(ctx))
	got, err := smorm.Find[ormCategory](ctx, newEM(), leaf.ID)
	must(t, err)
	if got.Parent == nil || got.Parent.ID != child.ID {
		t.Fatal("self reference")
	}
	// Mutual references are broken with a deferred update.
	a := &ormPerson{Name: "a"}
	b := &ormPerson{Name: "b", Friend: a}
	a.Friend = b
	must(t, em.Persist(a))
	must(t, em.Persist(b))
	must(t, em.Flush(ctx))
	em2 := newEM()
	la, err := smorm.Find[ormPerson](ctx, em2, a.ID)
	must(t, err)
	if la.Friend == nil || la.Friend.ID != b.ID {
		t.Fatal("deferred FK not written")
	}
	lb, err := smorm.Find[ormPerson](ctx, em2, b.ID)
	must(t, err)
	if lb.Friend != la {
		t.Fatal("cycle through identity map")
	}
	// Delete both: cycle broken by nulling a FK first.
	must(t, em2.Remove(la))
	must(t, em2.Remove(lb))
	must(t, em2.Flush(ctx))
	if n := Count(t, db, "orm_person"); n != 0 {
		t.Fatalf("persons left: %d", n)
	}
	// Delete order parent/child.
	em3 := newEM()
	r, _ := smorm.Find[ormCategory](ctx, em3, root.ID)
	c, _ := smorm.Find[ormCategory](ctx, em3, child.ID)
	l, _ := smorm.Find[ormCategory](ctx, em3, leaf.ID)
	for _, x := range []*ormCategory{r, c, l} {
		must(t, em3.Remove(x))
	}
	must(t, em3.Flush(ctx))
	if n := Count(t, db, "orm_category"); n != 0 {
		t.Fatalf("categories left: %d", n)
	}
}

func testORMRefreshDetach(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	a := &ormAuthor{name: "x"}
	must(t, em.Persist(a))
	must(t, em.Flush(ctx))
	a.name = "changed"
	must(t, em.Refresh(ctx, a))
	if a.name != "x" {
		t.Fatal("Refresh must discard changes")
	}
	a.name = "detached change"
	must(t, em.Detach(a))
	if em.Contains(a) || em.State(a) != 3 {
		t.Fatal("detached")
	}
	must(t, em.Flush(ctx))
	if err := em.Persist(a); err == nil {
		t.Fatal("persisting a detached entity must fail")
	}
	if err := em.Refresh(ctx, a); err == nil {
		t.Fatal("refresh of detached must fail")
	}
	b := &ormAuthor{name: "y"}
	must(t, em.Persist(b))
	em.Clear()
	must(t, em.Flush(ctx))
	if n := Count(t, db, "orm_author"); n != 1 {
		t.Fatalf("Clear must drop pending inserts, got %d rows", n)
	}
	// Removing a new entity just forgets it.
	c := &ormAuthor{name: "z"}
	must(t, em.Persist(c))
	must(t, em.Remove(c))
	must(t, em.Flush(ctx))
	if n := Count(t, db, "orm_author"); n != 1 {
		t.Fatal("removed new entity was inserted")
	}
	// Persist after remove keeps it.
	l, err := smorm.Find[ormAuthor](ctx, em, a.ID())
	must(t, err)
	must(t, em.Remove(l))
	must(t, em.Persist(l))
	must(t, em.Flush(ctx))
	if n := Count(t, db, "orm_author"); n != 1 {
		t.Fatal("re-persisted entity was deleted")
	}
	if _, err := smorm.Find[ormAuthor](ctx, em, int64(999999)); !errors.Is(err, smorm.ErrNotFound) {
		t.Fatal("not found")
	}
	if err := em.Persist(ormAuthor{}); !errors.Is(err, smorm.ErrNotEntity) {
		t.Fatalf("non pointer: %v", err)
	}
}

func testORMFailures(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	newEM := setupORM(t, db)
	em := newEM()
	must(t, em.Persist(&ormAuthor{name: "dup"}))
	must(t, em.Flush(ctx))
	must(t, em.Persist(&ormAuthor{name: "ok"}))
	must(t, em.Persist(&ormAuthor{name: "dup"}))
	if err := em.Flush(ctx); err == nil {
		t.Fatal("unique violation expected")
	}
	if em.IsOpen() || !errors.Is(em.Persist(&ormAuthor{}), smorm.ErrClosed) || !errors.Is(em.Flush(ctx), smorm.ErrClosed) {
		t.Fatal("entity manager must be closed after a failed flush")
	}
	if n := Count(t, db, "orm_author"); n != 1 {
		t.Fatalf("failed flush must roll back, rows = %d", n)
	}
	em2 := newEM()
	boom := errors.New("boom")
	err := em2.Transactional(ctx, func(ctx context.Context, em *smorm.EntityManager) error {
		must(t, em.Persist(&ormAuthor{name: "tx"}))
		if err := em.Flush(ctx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if n := Count(t, db, "orm_author"); n != 1 {
		t.Fatal("Transactional must roll back")
	}
	must(t, em2.Transactional(ctx, func(ctx context.Context, em *smorm.EntityManager) error {
		return em.Persist(&ormAuthor{name: "committed"})
	}))
	if n := Count(t, db, "orm_author"); n != 2 {
		t.Fatal("Transactional must flush and commit")
	}
}

func testORMGenerators(t *testing.T, db *dbal.DB) {
	ctx := context.Background()
	var extra []any
	seq := db.Dialect().Features().Has(dialect.FeatureSequences)
	if seq {
		extra = append(extra, ormTicket{})
	}
	newEM := setupORM(t, db, extra...)
	em := newEM()
	// Composite, application assigned identifiers.
	l1 := &ormLine{OrderNo: 1, LineNo: 1, Qty: 3}
	l2 := &ormLine{OrderNo: 1, LineNo: 2, Qty: 4}
	must(t, em.Persist(l1))
	must(t, em.Persist(l2))
	if err := em.Persist(&ormLine{OrderNo: 1, LineNo: 1}); err == nil {
		t.Fatal("duplicate identifier must fail")
	}
	if err := em.Persist(&ormLine{}); err == nil {
		t.Fatal("missing application identifier must fail")
	}
	must(t, em.Flush(ctx))
	got, err := smorm.Find[ormLine](ctx, newEM(), 1, 2)
	must(t, err)
	if got.Qty != 4 {
		t.Fatalf("composite find = %+v", got)
	}
	if _, err := smorm.Find[ormLine](ctx, em, 1); err == nil {
		t.Fatal("wrong identifier count")
	}
	// Identity-only entity (INSERT DEFAULT VALUES).
	c1, c2 := &ormCounter{}, &ormCounter{}
	must(t, em.Persist(c1))
	must(t, em.Persist(c2))
	must(t, em.Flush(ctx))
	if c1.ID == 0 || c2.ID == c1.ID {
		t.Fatalf("identity ids %d %d", c1.ID, c2.ID)
	}
	if seq {
		t1, t2 := &ormTicket{Subject: "a"}, &ormTicket{Subject: "b"}
		must(t, em.Persist(t1))
		must(t, em.Persist(t2))
		must(t, em.Flush(ctx))
		if t1.ID < 1 || t2.ID != t1.ID+1 {
			t.Fatalf("sequence ids %d %d", t1.ID, t2.ID)
		}
	}
	_ = uuid.Nil
}
