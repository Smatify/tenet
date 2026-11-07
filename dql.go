package tenet

import (
	"context"
	"fmt"
	"reflect"
)

// Params are DQL parameters (":name" -> "name", "?1" -> "1").
type Params = map[string]any

// DQL parses a query in the Doctrine Query Language subset (see orm.DQL)
// selecting T, e.g.
//
//	tenet.DQL[Post](em, "SELECT p FROM Post p JOIN p.author a WHERE a.name = :n", tenet.Params{"n": "ann"})
func DQL[T any](em *EntityManager, dql string, params Params) (*Query[T], error) {
	res, err := em.ParseDQL(dql, params)
	if err != nil {
		return nil, err
	}
	if res.Count {
		return nil, fmt.Errorf("dql: COUNT queries return a number; use DQLCount")
	}
	if got := res.Query.Meta().GoType; got != reflect.TypeFor[T]() {
		return nil, fmt.Errorf("dql: the query selects %s, not %s", got.Name(), reflect.TypeFor[T]().Name())
	}
	return &Query[T]{q: res.Query}, nil
}

// DQLCount runs a SELECT COUNT(alias) ... query.
func DQLCount(ctx context.Context, em *EntityManager, dql string, params Params) (int, error) {
	res, err := em.ParseDQL(dql, params)
	if err != nil {
		return 0, err
	}
	return res.Query.Count(ctx)
}
