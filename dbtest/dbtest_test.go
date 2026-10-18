package dbtest

import (
	"testing"

	"github.com/smatify/smorm/internal/testdialect"
)

func TestBind(t *testing.T) {
	d := testdialect.New()
	d.Dollar = true
	if got := Bind(d, "a = ? AND b = ?"); got != "a = $1 AND b = $2" {
		t.Fatalf("got %s", got)
	}
}

func TestCasesRegistered(t *testing.T) {
	if len(cases) < 5 {
		t.Fatalf("only %d cases registered", len(cases))
	}
	n := len(cases)
	Register(Case{Name: "x"})
	if len(cases) != n+1 {
		t.Fatal("Register did not add")
	}
	cases = cases[:n]
}
