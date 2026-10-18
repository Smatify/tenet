package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = "internal/fixture/blog"

func TestRenderMatchesCommittedFile(t *testing.T) {
	src, err := Render(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(filepath.Join(fixture, FileName), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(filepath.Join(fixture, FileName))
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1)", err)
	}
	if string(src) != string(want) {
		t.Fatalf("generated code is stale; run with UPDATE_GOLDEN=1\n%s", src)
	}
	for _, s := range []string{`"title": {`, `"Email": {`, `PostFields`, `AuthorFields`, `Title:`, `"Posts": {`} {
		if !strings.Contains(string(src), s) {
			t.Errorf("missing %q", s)
		}
	}
	for _, s := range []string{`"cache"`, `NotAnEntity`, `"UUIDID.id"`} {
		if strings.Contains(string(src), s) {
			t.Errorf("unexpected %q", s)
		}
	}
}

func TestGenerateWritesAndRemoves(t *testing.T) {
	dir := t.TempDir()
	mod := "module example.com/x\n\ngo 1.27\n"
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", mod)
	write("x.go", "package x\n\ntype A struct{ B int }\n")
	stale := filepath.Join(dir, FileName)
	write(FileName, "package x\n")
	out, err := Generate(dir)
	if err != nil || out != "" {
		t.Fatalf("Generate = %q, %v", out, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale generated file must be removed")
	}
	if _, err := Generate(dir); err != nil {
		t.Fatal("removing a missing file is fine")
	}
	write("x.go", "package x\n\nfunc broken( {\n")
	if _, err := Generate(dir); err == nil {
		t.Fatal("syntax errors must be reported")
	}
	if _, err := Render(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing dir must fail")
	}
}

func TestGenerateFixtureInPlace(t *testing.T) {
	out, err := Generate(fixture)
	if err != nil || filepath.Base(out) != FileName {
		t.Fatalf("Generate = %q, %v", out, err)
	}
}

func TestExportName(t *testing.T) {
	if exportName("id", nil) != "ID" || exportName("title", nil) != "Title" ||
		exportName("title", []refData{{Exported: "Title"}}) != "Title_" {
		t.Fatal("exportName")
	}
}
