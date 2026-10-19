package astedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const src = `// Package blog is a test.
package blog

import "fmt"

// Post is a blog post.
type Post struct {
	title string // the title
}

// Title returns the title.
func (p *Post) Title() string { return fmt.Sprint(p.title) }
`

func TestEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Package() != "blog" || f.Struct("Post") == nil || f.Struct("Nope") != nil || !f.HasField("Post", "Title") || f.HasField("Post", "body") || f.HasField("Nope", "x") {
		t.Fatal("lookups")
	}
	if !f.HasMethod("Post", "Title") || f.HasMethod("Post", "Body") || f.HasMethod("Other", "Title") {
		t.Fatal("HasMethod")
	}
	if err := f.AddField("Post", "body string `orm:\"type:text\"`", "the content"); err != nil {
		t.Fatal(err)
	}
	if err := f.AddField("Nope", "x int", ""); err == nil {
		t.Fatal("unknown struct")
	}
	if err := f.AddField("Post", "x int int int", ""); err == nil {
		t.Fatal("invalid field")
	}
	if err := f.AddDecls("// Body returns the body.\nfunc (p *Post) Body() string { return p.body }\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.AddDecls("func ("); err == nil {
		t.Fatal("invalid decls")
	}
	f.AddImport("time", "")
	f.AddImport("fmt", "")
	f.AddImport("example.com/x", "ex")
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	for _, want := range []string{
		"// Package blog is a test.", "title string // the title", "// the content\n\tbody string `orm:\"type:text\"`",
		"// Body returns the body.\nfunc (p *Post) Body() string", "import (\n\tex \"example.com/x\"\n\t\"fmt\"\n\t\"time\"\n)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Count(s, `"fmt"`) != 1 {
		t.Fatal("duplicate import")
	}
}

func TestAddImportWithoutImports(t *testing.T) {
	f, err := Parse("x.go", []byte("package x\n\ntype A struct{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	f.AddImport("time", "")
	f.AddImport("context", "")
	out, err := f.Source()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "import (\n\t\"context\"\n\t\"time\"\n)") {
		t.Fatalf("got\n%s", out)
	}
	if _, err := Parse("bad.go", []byte("package")); err == nil {
		t.Fatal("parse error")
	}
	if _, err := Open("/does/not/exist.go"); err == nil {
		t.Fatal("open error")
	}
}
