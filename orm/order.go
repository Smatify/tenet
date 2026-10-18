package orm

import (
	"fmt"

	"github.com/smatify/smorm/mapping"
)

// commitOrder sorts entities scheduled for insertion so that every entity is
// inserted after the entities its foreign keys point to. Cycles are broken at
// a nullable foreign key: the entity is inserted with NULL and updated after
// all inserts (recorded in entry.deferred).
func commitOrder(em *EntityManager, inserts []*entry) ([]*entry, error) {
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
