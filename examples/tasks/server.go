package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/uuid"
)

type server struct{ db *dbal.DB }

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tasks", s.list)
	mux.HandleFunc("POST /tasks", s.create)
	mux.HandleFunc("PATCH /tasks/{id}", s.update)
	mux.HandleFunc("DELETE /tasks/{id}", s.remove)
	// A getter that fails to load lazily panics; answer with a 500.
	return tenet.RecoverLazyLoad(mux, nil)
}

// em returns a new entity manager for the request (one per request, never
// shared), limited to the caller's workspace.
func (s *server) em(w http.ResponseWriter, r *http.Request) (*tenet.EntityManager, string, bool) {
	ws := r.Header.Get("X-Workspace")
	if ws == "" {
		http.Error(w, "missing X-Workspace", http.StatusUnauthorized)
		return nil, "", false
	}
	em := tenet.NewEntityManager(s.db, tenet.WithContext(r.Context()))
	em.Filters().Enable("workspace", tenet.ColumnFilter("workspace", ws))
	return em, ws, true
}

type taskJSON struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Done     bool       `json:"done"`
	Due      *time.Time `json:"due,omitempty"`
	Assignee string     `json:"assignee,omitempty"`
}

func view(t *Task) taskJSON {
	v := taskJSON{ID: t.ID().String(), Title: t.Title, Done: t.Done, Due: t.Due}
	if t.Assignee != nil {
		v.Assignee = t.Assignee.Name
	}
	return v
}

type pageJSON struct {
	Items []taskJSON `json:"items"`
	Page  int        `json:"page"`
	Pages int        `json:"pages"`
	Total int        `json:"total"`
}

// list returns a page of open tasks first, then by title.
func (s *server) list(w http.ResponseWriter, r *http.Request) {
	em, _, ok := s.em(w, r)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	q := tenet.NewQuery[Task](em).
		OrderBy("Done", tenet.Asc).
		OrderBy("Title", tenet.Asc).
		With("Assignee") // joined into the query: no query per task
	page, err := tenet.Paginate(r.Context(), q, n, 20)
	if err != nil {
		fail(w, err)
		return
	}
	out := pageJSON{Items: []taskJSON{}, Page: page.Page, Pages: page.TotalPages, Total: page.Total}
	for _, t := range page.Items {
		out.Items = append(out.Items, view(t))
	}
	reply(w, http.StatusOK, out)
}

type taskInput struct {
	Title    *string    `json:"title"`
	Done     *bool      `json:"done"`
	Due      *time.Time `json:"due"`
	Assignee *int64     `json:"assignee"`
}

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	em, ws, ok := s.em(w, r)
	if !ok {
		return
	}
	t := &Task{Workspace: ws}
	if !s.apply(w, r, em, t) {
		return
	}
	if t.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if err := em.Persist(t); err != nil {
		fail(w, err)
		return
	}
	if err := em.Flush(r.Context()); err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, view(t))
}

func (s *server) update(w http.ResponseWriter, r *http.Request) {
	em, _, ok := s.em(w, r)
	if !ok {
		return
	}
	t, ok := find(w, r, em)
	if !ok || !s.apply(w, r, em, t) {
		return
	}
	if err := em.Flush(r.Context()); err != nil { // writes only the changed columns
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view(t))
}

func (s *server) remove(w http.ResponseWriter, r *http.Request) {
	em, _, ok := s.em(w, r)
	if !ok {
		return
	}
	t, ok := find(w, r, em)
	if !ok {
		return
	}
	if err := em.Remove(t); err != nil {
		fail(w, err)
		return
	}
	if err := em.Flush(r.Context()); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// find loads the task of the path; tasks of other workspaces and deleted
// ones are not found.
func find(w http.ResponseWriter, r *http.Request, em *tenet.EntityManager) (*Task, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return nil, false
	}
	t, err := tenet.RepositoryFor[Task](em).FindOneBy(r.Context(), tenet.Criteria{"id": id})
	if err != nil {
		fail(w, err)
		return nil, false
	}
	return t, true
}

// apply sets the fields given in the request body.
func (s *server) apply(w http.ResponseWriter, r *http.Request, em *tenet.EntityManager, t *Task) bool {
	var in taskInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	if in.Title != nil {
		t.Title = *in.Title
	}
	if in.Done != nil {
		t.Done = *in.Done
	}
	if in.Due != nil {
		t.Due = in.Due
	}
	if in.Assignee != nil {
		u, err := tenet.RepositoryFor[User](em).FindOneBy(r.Context(), tenet.Criteria{"id": *in.Assignee})
		if errors.Is(err, tenet.ErrNotFound) {
			http.Error(w, "unknown assignee", http.StatusBadRequest)
			return false
		}
		if err != nil {
			fail(w, err)
			return false
		}
		t.Assignee = u
	}
	return true
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, tenet.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
