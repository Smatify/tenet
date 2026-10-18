package smorm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoverLazyLoad(t *testing.T) {
	lazy := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(&LazyLoadError{Entity: "Post", Err: ErrNotFound})
	})
	rr := httptest.NewRecorder()
	RecoverLazyLoad(lazy, nil).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 500 {
		t.Fatalf("default status = %d", rr.Code)
	}
	var got *LazyLoadError
	rr = httptest.NewRecorder()
	RecoverLazyLoad(lazy, func(w http.ResponseWriter, _ *http.Request, err *LazyLoadError) {
		got = err
		w.WriteHeader(http.StatusNotFound)
	}).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 404 || got.Entity != "Post" {
		t.Fatal("custom handler")
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	rr = httptest.NewRecorder()
	RecoverLazyLoad(ok, nil).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 204 {
		t.Fatal("pass through")
	}
	defer func() {
		if r := recover(); r == nil || r.(error).Error() != "other" {
			t.Fatal("other panics must propagate")
		}
	}()
	other := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(errors.New("other")) })
	RecoverLazyLoad(other, nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestTouchAndCatch(t *testing.T) {
	Touch(42) // not an entity: no-op
	if err := CatchLazy(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
