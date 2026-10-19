package scaffold

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func project(t *testing.T) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInit(t *testing.T) {
	for name := range Drivers {
		dir := project(t)
		files, err := Init(Options{Dir: dir, Dialect: name})
		if err != nil || len(files) != 3 {
			t.Fatalf("%s: %v %v", name, files, err)
		}
		for _, f := range files {
			if _, err := parser.ParseFile(token.NewFileSet(), f, nil, 0); err != nil {
				t.Fatalf("%s: %s does not parse: %v", name, f, err)
			}
		}
		main, _ := os.ReadFile(filepath.Join(dir, "cmd/console/main.go"))
		for _, s := range []string{`_ "example.com/app/internal/entity"`, `maker.Register(app)`, Drivers[name].DriverImport, Drivers[name].Dialect + "." + Drivers[name].DialectNew + "()"} {
			if !strings.Contains(string(main), s) {
				t.Errorf("%s: main.go misses %q", name, s)
			}
		}
		again, err := Init(Options{Dir: dir, Dialect: name})
		if err != nil || len(again) != 0 {
			t.Fatal("existing files must not be overwritten")
		}
	}
}

func TestInitErrors(t *testing.T) {
	if _, err := Init(Options{Dir: project(t), Dialect: "oracle"}); err == nil {
		t.Fatal("unknown dialect")
	}
	if _, err := Init(Options{Dir: t.TempDir(), Dialect: "sqlite"}); err == nil {
		t.Fatal("missing go.mod")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("go 1.27\n"), 0o644)
	if _, err := ModulePath(dir); err == nil {
		t.Fatal("go.mod without module")
	}
}
