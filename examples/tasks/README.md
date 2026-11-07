# Example: tasks

A JSON API over `net/http` for the tasks of several workspaces, in the Go
style (exported fields, no generated code), on SQLite:

```bash
go run .
curl -H 'X-Workspace: acme' -d '{"title":"Write docs","assignee":1}' localhost:8080/tasks
curl -H 'X-Workspace: acme' localhost:8080/tasks
```

[server.go](server.go):

- A new entity manager per request, never shared between requests.
- A filter on the `workspace` column (`tenet.ColumnFilter`) keeps the
  workspaces apart: queries, pages and lookups only see the caller's rows.
- `DELETE` removes softly (`tenet.SoftDelete`): the row keeps a `deleted_at`
  and disappears from queries.
- The list is a page (`tenet.Paginate`) with the assignees joined into the
  query (`With`), `PATCH` writes only the changed columns.
- `tenet.RecoverLazyLoad` answers a failed lazy load with a 500.

`go test` runs the API with `httptest` against SQLite in memory.
