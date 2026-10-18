package dbal

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// QueryEvent describes a single statement sent to the database.
type QueryEvent struct {
	DB        IDB
	Query     string
	Args      []any
	Operation string // first SQL keyword in upper case, e.g. SELECT
	StartTime time.Time
	Result    sql.Result // nil for queries
	Err       error
	Stash     map[any]any // free storage for hooks
}

// Duration returns the time elapsed since the query started.
func (e *QueryEvent) Duration() time.Duration { return time.Since(e.StartTime) }

// QueryHook observes every statement. BeforeQuery may return a derived context
// (e.g. carrying a tracing span) which is passed to AfterQuery.
type QueryHook interface {
	BeforeQuery(ctx context.Context, ev *QueryEvent) context.Context
	AfterQuery(ctx context.Context, ev *QueryEvent)
}

// QueryHookFuncs adapts plain functions to QueryHook; nil funcs are skipped.
type QueryHookFuncs struct {
	Before func(ctx context.Context, ev *QueryEvent) context.Context
	After  func(ctx context.Context, ev *QueryEvent)
}

// BeforeQuery implements QueryHook.
func (h QueryHookFuncs) BeforeQuery(ctx context.Context, ev *QueryEvent) context.Context {
	if h.Before != nil {
		return h.Before(ctx, ev)
	}
	return ctx
}

// AfterQuery implements QueryHook.
func (h QueryHookFuncs) AfterQuery(ctx context.Context, ev *QueryEvent) {
	if h.After != nil {
		h.After(ctx, ev)
	}
}

func operation(query string) string {
	q := strings.TrimLeft(query, " \t\r\n(")
	if i := strings.IndexAny(q, " \t\r\n("); i >= 0 {
		q = q[:i]
	}
	return strings.ToUpper(q)
}

func (db *DB) beforeQuery(ctx context.Context, idb IDB, query string, args []any) (context.Context, *QueryEvent) {
	hooks := db.hooks.Load()
	if hooks == nil || len(*hooks) == 0 {
		return ctx, nil
	}
	ev := &QueryEvent{DB: idb, Query: query, Args: args, Operation: operation(query), StartTime: time.Now()}
	for _, h := range *hooks {
		ctx = h.BeforeQuery(ctx, ev)
	}
	return ctx, ev
}

func (db *DB) afterQuery(ctx context.Context, ev *QueryEvent, res sql.Result, err error) {
	if ev == nil {
		return
	}
	ev.Result = res
	ev.Err = err
	hooks := *db.hooks.Load()
	for i := len(hooks) - 1; i >= 0; i-- {
		hooks[i].AfterQuery(ctx, ev)
	}
}
