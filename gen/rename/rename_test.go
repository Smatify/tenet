package rename

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const src = "package p\n\n// T keeps its comment.\ntype T struct {\n\tA string `old:\"length:10\" json:\"a\"`\n\tB int    `json:\"b\" old:\"-\"`\n\tC int    \"old:\\\"x\\\"\"\n\tD int    `json:\"d\"`\n\tE int\n}\n"

func TestSource(t *testing.T) {
	out, changed, err := Source("t.go", []byte(src), "old", "fresh")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	s := string(out)
	for _, want := range []string{"// T keeps its comment.", "`fresh:\"length:10\" json:\"a\"`", "`json:\"b\" fresh:\"-\"`", "`fresh:\"x\"`", "`json:\"d\"`"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if _, changed, _ := Source("t.go", []byte(src), "nope", "x"); changed {
		t.Fatal("unrelated key")
	}
	if _, _, err := Source("bad.go", []byte("package"), "a", "b"); err == nil {
		t.Fatal("parse error")
	}
}

func TestRenameKey(t *testing.T) {
	cases := map[string]string{
		`old:"a"`:                `new:"a"`,
		`  old:"a\"b"  json:"x"`: `  new:"a\"b"  json:"x"`,
		`weird`:                  `weird`,
		`old:"unterminated`:      `old:"unterminated`,
		`older:"a" old:"b"`:      `older:"a" new:"b"`,
	}
	for in, want := range cases {
		if got, _ := renameKey(in, "old", "new"); got != want {
			t.Errorf("renameKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTags(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", src)
	write("sub/b.go", src)
	write("vendor/v.go", src)
	write("testdata/x.go", src)
	write(".hidden/h.go", src)
	write("notes.txt", "old:\"x\"")
	write("c.go", "package p\n")
	changed, err := Tags(dir, "old", "new")
	if err != nil || len(changed) != 2 {
		t.Fatalf("changed = %v, %v", changed, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "vendor/v.go")); !strings.Contains(string(b), "old:") {
		t.Fatal("vendor must be skipped")
	}
	if _, err := Tags(dir, "", "x"); err == nil {
		t.Fatal("empty key")
	}
	if _, err := Tags(dir, "a", `b"c`); err == nil {
		t.Fatal("invalid key")
	}
	write("broken.go", "package")
	if _, err := Tags(dir, "new", "other"); err == nil {
		t.Fatal("parse errors must surface")
	}
	if _, err := File(filepath.Join(dir, "missing.go"), "a", "b"); err == nil {
		t.Fatal("missing file")
	}
}
