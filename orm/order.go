package orm

import (
	"fmt"

	"github.com/smatify/smorm/mapping"
)

// commitOrder sorts entities scheduled for insertion so that every entity is
// inserted after the entities its foreign keys point to. When the types
// depend on each other without cycles or self-references, the entities go
// type by type in the order they were persisted, so each type is one run
// of batched inserts (entity by entity, parents and children took turns:
// one statement of a few rows each). Otherwise cycles are broken at a
// nullable foreign key: the entity is inserted with NULL and updated after
// all inserts (recorded in entry.deferred).
func commitOrder(em *EntityManager, inserts []*entry) ([]*entry, error) {
	if out, ok := typeOrder(em, inserts); ok {
		return out, nil
	}
	return entryOrder(em, inserts)
}

// typeOrder sorts inserts by the dependencies of their types; ok is false
// when a type refers to itself or the types form a cycle among inserts.
func typeOrder(em *EntityManager, inserts []*entry) ([]*entry, bool) {
	pending := make(map[*entry]bool, len(inserts))
	for _, e := range inserts {
		pending[e] = true
	}
	type meta = *mapping.Entity
	var types []meta
	byType := map[meta][]*entry{}
	deps := map[meta]map[meta]bool{}
	for _, e := range inserts {
		if _, ok := byType[e.meta]; !ok {
			types = append(types, e.meta)
			deps[e.meta] = map[meta]bool{}
		}
		byType[e.meta] = append(byType[e.meta], e)
		for _, f := range e.meta.Fields {
			if f.Assoc == nil {
				continue
			}
			target := em.assocTarget(e, f.Assoc)
			if target == nil {
				continue
			}
			dep := em.entryOf(f.Assoc.Target, target)
			if dep == nil || !pending[dep] {
				continue
			}
			if dep.meta == e.meta {
				return nil, false
			}
			deps[e.meta][dep.meta] = true
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[meta]int{}
	out := make([]*entry, 0, len(inserts))
	var visit func(t meta) bool
	visit = func(t meta) bool {
		state[t] = visiting
		for _, d := range types { // in persist order, for a stable result
			if !deps[t][d] {
				continue
			}
			switch state[d] {
			case visiting:
				return false
			case unvisited:
				if !visit(d) {
					return false
				}
			}
		}
		state[t] = done
		out = append(out, byType[t]...)
		return true
	}
	for _, t := range types {
		if state[t] == unvisited && !visit(t) {
			return nil, false
		}
	}
	return out, true
}

// entryOrder sorts inserts entity by entity (depth first along their
// foreign keys), breaking cycles at nullable foreign keys.
func entryOrder(em *EntityManager, inserts []*entry) ([]*entry, error) {
	pending := make(map[*entry]bool, len(inserts))
	for _, e := range inserts {
		pending[e] = true
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[*entry]int{}
	out := make([]*entry, 0, len(inserts))
	var visit func(e *entry) error
	visit = func(e *entry) error {
		state[e] = visiting
		for _, f := range e.meta.Fields {
			if f.Assoc == nil {
				continue
			}
			target := em.assocTarget(e, f.Assoc)
			if target == nil {
				continue
			}
			dep := em.entryOf(f.Assoc.Target, target)
			if dep == nil || !pending[dep] {
				continue
			}
			switch state[dep] {
			case visiting:
				if !f.Nullable {
					return fmt.Errorf("%s: cannot order inserts: %s.%s and %s reference each other through NOT NULL foreign keys",
						brandName, e.meta.Name, f.Assoc.Name, dep.meta.Name)
				}
				e.deferred = append(e.deferred, f)
			case unvisited:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		state[e] = done
		out = append(out, e)
		return nil
	}
	for _, e := range inserts {
		if state[e] == unvisited {
			if err := visit(e); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

type fkRef struct {
	e *entry
	f *mapping.Field
}

// deleteOrder sorts removed entities so that referencing rows are deleted
// before the rows they reference. Cycles are broken by setting a nullable
// foreign key to NULL before deleting (returned as nullify).
func deleteOrder(em *EntityManager, removes []*entry) ([]*entry, []fkRef, error) {
	pending := make(map[*entry]bool, len(removes))
	for _, e := range removes {
		pending[e] = true
	}
	// Build "must be deleted before" edges: referencing -> referenced.
	before := map[*entry][]fkRef{} // referenced entry -> referencing entries
	for _, e := range removes {
		for _, f := range e.meta.Fields {
			if f.Assoc == nil {
				continue
			}
			t := em.assocTarget(e, f.Assoc)
			if t == nil {
				continue
			}
			if dep := em.entryOf(f.Assoc.Target, t); dep != nil && pending[dep] && dep != e {
				before[dep] = append(before[dep], fkRef{e: e, f: f})
			}
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[*entry]int{}
	var out []*entry
	var nullify []fkRef
	var visit func(e *entry) error
	visit = func(e *entry) error {
		state[e] = visiting
		for _, r := range before[e] {
			switch state[r.e] {
			case visiting:
				if !r.f.Nullable {
					return fmt.Errorf("%s: cannot order deletes: %s.%s forms a cycle through a NOT NULL foreign key",
						brandName, r.e.meta.Name, r.f.Assoc.Name)
				}
				nullify = append(nullify, r)
			case unvisited:
				if err := visit(r.e); err != nil {
					return err
				}
			}
		}
		state[e] = done
		out = append(out, e)
		return nil
	}
	for _, e := range removes {
		if state[e] == unvisited {
			if err := visit(e); err != nil {
				return nil, nil, err
			}
		}
	}
	return out, nullify, nil
}
