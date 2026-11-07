// Command tasks serves a JSON API for the tasks of several workspaces:
//
//	go run . &
//	curl -H 'X-Workspace: acme' -d '{"title":"Write docs","assignee":1}' localhost:8080/tasks
//	curl -H 'X-Workspace: acme' localhost:8080/tasks
package main

import (
	"context"
	"log"
	"net/http"

	_ "modernc.org/sqlite"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect/sqlitedialect"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/schema"
)

func main() {
	ctx := context.Background()
	db, err := open(ctx, "file:tasks.db?_pragma=foreign_keys(1)")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := seed(ctx, db); err != nil {
		log.Fatal(err)
	}
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", (&server{db: db}).handler()))
}

// open opens the database and creates missing tables. An application keeps
// its schema in migrations (see examples/blog).
func open(ctx context.Context, dsn string) (*dbal.DB, error) {
	db, err := dbal.Open("sqlite", dsn, sqlitedialect.New())
	if err != nil {
		return nil, err
	}
	db.SQL().SetMaxOpenConns(1)
	reg := mapping.NewRegistry()
	if err := reg.Register(&User{}, &Task{}); err != nil {
		return nil, err
	}
	stmts, err := schema.NewDDL(db.Dialect()).CreateSchema(mapping.BuildSchema(reg.All()))
	if err != nil {
		return nil, err
	}
	var n int
	if err := db.SQL().QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = 'task'").Scan(&n); err != nil || n > 0 {
		return db, err
	}
	for _, s := range stmts {
		if _, err := db.SQL().ExecContext(ctx, s); err != nil {
			return nil, err
		}
	}
	return db, nil
}

// seed adds a user to the workspaces acme and globex when there is none.
func seed(ctx context.Context, db *dbal.DB) error {
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	if n, err := tenet.NewQuery[User](em).Count(ctx); err != nil || n > 0 {
		return err
	}
	for _, u := range []*User{{Workspace: "acme", Name: "Ann"}, {Workspace: "globex", Name: "Bob"}} {
		if err := em.Persist(u); err != nil {
			return err
		}
	}
	return em.Flush(ctx)
}
