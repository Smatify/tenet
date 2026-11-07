package orm

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/smatify/tenet/dialect"
	"github.com/smatify/tenet/internal/entity"
	"github.com/smatify/tenet/mapping"
)

type hooked struct {
	entity.Header `tenet:"table:h"`
	ID            int64
	Name          string
	Slug          string
	log           *[]string `tenet:"-"`
	fail          string    `tenet:"-"`
}

func (h *hooked) rec(s string) error {
	if h.log != nil {
		*h.log = append(*h.log, s)
	}
	if h.fail == s {
		return errors.New("hook " + s + " failed")
	}
	return nil
}
func (h *hooked) PrePersist(context.Context) error {
	h.Slug = strings.ToLower(h.Name)
	return h.rec("prePersist")
}
func (h *hooked) PostPersist(context.Context) error { return h.rec("postPersist") }
func (h *hooked) PreUpdate(_ context.Context, cs *ChangeSet) error {
	if cs.Has("Name") {
		h.Slug = strings.ToLower(h.Name)
	}
	return h.rec("preUpdate:" + strings.Join(cs.Fields(), ","))
}
func (h *hooked) PostUpdate(context.Context) error { return h.rec("postUpdate") }
func (h *hooked) PreRemove(context.Context) error  { return h.rec("preRemove") }
func (h *hooked) PostRemove(context.Context) error { return h.rec("postRemove") }
func (h *hooked) PostLoad(context.Context) error   { return h.rec("postLoad") }

func hookEM(t *testing.T) (*EntityManager, *[]string, func() []string) {
	em, rec := newUnitEM(t, dialect.MySQL)
	if err := em.reg.Register(hooked{}); err != nil {
		t.Fatal(err)
	}
	var log []string
	em.Events().On(func(_ context.Context, a *EventArgs) error {
		name := "-"
		if a.Meta != nil {
			name = a.Meta.Name
		}
		log = append(log, "listener:"+name)
		return nil
	}, EventPostPersist, EventOnFlush, EventPostFlush)
	return em, &log, rec.Queries
}

func TestLifecycleHooks(t *testing.T) {
	ctx := context.Background()
	em, log, queries := hookEM(t)
	h := &hooked{Name: "Hello"}
	h.log = log
	if err := em.Persist(h); err != nil {
		t.Fatal(err)
	}
	if err := em.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if h.Slug != "hello" {
		t.Fatal("PrePersist must run before the insert")
	}
	if !slices.Equal(*log, []string{"prePersist", "listener:-", "postPersist", "listener:hooked", "listener:-"}) {
		t.Fatalf("log = %v", *log)
	}
	*log = nil
	h.Name = "World"
	if err := em.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*log, []string{"preUpdate:Name", "listener:-", "postUpdate", "listener:-"}) || h.Slug != "world" {
		t.Fatalf("log = %v slug=%s", *log, h.Slug)
	}
	q := queries()
	if last := q[len(q)-2]; !strings.Contains(last, "`slug`") || !strings.Contains(last, "`name`") {
		t.Fatalf("PreUpdate modification must be written: %s", last)
	}
	*log = nil
	if err := em.Remove(h); err != nil {
		t.Fatal(err)
	}
	if err := em.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*log, []string{"preRemove", "listener:-", "postRemove", "listener:-"}) {
		t.Fatalf("log = %v", *log)
	}
}

func TestHookErrors(t *testing.T) {
	ctx := context.Background()
	for _, stage := range []string{"prePersist", "postPersist"} {
		em, log, _ := hookEM(t)
		h := &hooked{Name: "x", log: log, fail: stage}
		_ = em.Persist(h)
		if err := em.Flush(ctx); err == nil || !strings.Contains(err.Error(), stage) {
			t.Fatalf("%s: err = %v", stage, err)
		}
		if stage == "prePersist" && !em.IsOpen() {
			t.Fatal("pre hooks run before the transaction and keep the manager open")
		}
		if stage == "postPersist" && em.IsOpen() {
			t.Fatal("post hooks run inside the transaction and close the manager")
		}
	}
	em, log, _ := hookEM(t)
	h := &hooked{Name: "x", log: log}
	_ = em.Persist(h)
	_ = em.Flush(ctx)
	h.fail = "preUpdate:Name"
	h.Name = "y"
	if err := em.Flush(ctx); err == nil {
		t.Fatal("preUpdate error")
	}
	h.fail = "preRemove"
	_ = em.Remove(h)
	if err := em.Flush(ctx); err == nil {
		t.Fatal("preRemove error")
	}
	em2, _, _ := hookEM(t)
	boom := errors.New("listener")
	em2.Events().On(func(context.Context, *EventArgs) error { return boom }, EventOnFlush)
	_ = em2.Persist(&hooked{Name: "z"})
	if err := em2.Flush(ctx); !errors.Is(err, boom) {
		t.Fatalf("onFlush error: %v", err)
	}
}

func TestPostLoadAndChangeSet(t *testing.T) {
	ctx := context.Background()
	em, rec := newUnitEM(t, dialect.Postgres)
	_ = em.reg.Register(hooked{})
	var loaded []string
	em.Events().On(func(_ context.Context, a *EventArgs) error {
		loaded = append(loaded, a.Entity.(*hooked).Name)
		return nil
	}, EventPostLoad)
	rec.Returns("FROM", []string{"id", "name", "slug"}, []driver.Value{int64(1), "n", "s"})
	v, err := em.Find(ctx, typeOf[hooked](), 1)
	if err != nil || v.(*hooked).Name != "n" || !slices.Equal(loaded, []string{"n"}) {
		t.Fatalf("v=%v err=%v loaded=%v", v, err, loaded)
	}
	e := em.entryOf(em.mustMeta(t), v)
	v.(*hooked).Name = "m"
	changed, vals, _ := em.changes(e)
	cs := newChangeSet(e, changed, vals)
	if !cs.Has("Name") || cs.Has("Slug") || cs.Old("Name") != "n" || cs.New("Name") != "m" {
		t.Fatalf("cs = %+v", cs)
	}
	boom := errors.New("pl")
	em.Events().On(func(context.Context, *EventArgs) error { return boom }, EventPostLoad)
	em.Clear()
	if _, err := em.Find(ctx, typeOf[hooked](), 1); !errors.Is(err, boom) {
		t.Fatalf("postLoad error: %v", err)
	}
	if em.Events() == nil || !em.Events().Has(EventPostLoad) || em.Events().Has(EventPreRemove) {
		t.Fatal("Has")
	}
	shared := NewEventManager()
	if New(em.db, WithEvents(shared)).Events() != shared {
		t.Fatal("WithEvents")
	}
}

func (em *EntityManager) mustMeta(t *testing.T) *mapping.Entity {
	m, err := em.reg.Get(typeOf[hooked]())
	if err != nil {
		t.Fatal(err)
	}
	return m
}
