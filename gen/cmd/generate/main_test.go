package main

import "testing"

func TestRun(t *testing.T) {
	if err := run([]string{"../../internal/fixture/blog"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"does-not-exist"}); err == nil {
		t.Fatal("expected error")
	}
}
