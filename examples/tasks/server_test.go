package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newServer(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	db, err := open(ctx, "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	return (&server{db: db}).handler()
}

func call(t *testing.T, h http.Handler, ws, method, path, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if ws != "" {
		req.Header.Set("X-Workspace", ws)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, rec.Body)
		}
	}
	return rec.Code
}

func TestTasks(t *testing.T) {
	h := newServer(t)

	var write, docs taskJSON
	if code := call(t, h, "acme", "POST", "/tasks", `{"title":"Write docs","assignee":1}`, &docs); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if docs.Assignee != "Ann" || docs.ID == "" {
		t.Fatalf("created %+v", docs)
	}
	call(t, h, "acme", "POST", "/tasks", `{"title":"Answer mail"}`, &write)
	var plan taskJSON
	call(t, h, "acme", "POST", "/tasks", `{"title":"Plan","due":"2026-12-01T09:00:00Z"}`, &plan)
	if code := call(t, h, "acme", "PATCH", "/tasks/"+write.ID, `{"done":true}`, nil); code != 200 {
		t.Fatalf("update: %d", code)
	}

	var page pageJSON
	call(t, h, "acme", "GET", "/tasks", "", &page)
	// Open tasks first, the one due first, the done one last.
	if page.Total != 3 || page.Items[0].Title != "Plan" || page.Items[1].Assignee != "Ann" || !page.Items[2].Done {
		t.Fatalf("list %+v", page)
	}

	// Other workspaces see nothing and can change nothing.
	call(t, h, "globex", "GET", "/tasks", "", &page)
	if page.Total != 0 {
		t.Fatalf("globex sees %+v", page)
	}
	if code := call(t, h, "globex", "PATCH", "/tasks/"+docs.ID, `{"done":true}`, nil); code != 404 {
		t.Fatalf("globex update: %d", code)
	}
	if code := call(t, h, "globex", "POST", "/tasks", `{"title":"x","assignee":1}`, nil); code != 400 {
		t.Fatalf("globex assigns Ann: %d", code)
	}

	// Deleted tasks disappear from the list.
	if code := call(t, h, "acme", "DELETE", "/tasks/"+write.ID, "", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	call(t, h, "acme", "GET", "/tasks", "", &page)
	if page.Total != 2 || page.Items[1].ID != docs.ID {
		t.Fatalf("after delete %+v", page)
	}
	if code := call(t, h, "acme", "DELETE", "/tasks/"+write.ID, "", nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}

	if code := call(t, h, "", "GET", "/tasks", "", nil); code != 401 {
		t.Fatalf("no workspace: %d", code)
	}
}
