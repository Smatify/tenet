package main

import (
	"bytes"

	"github.com/smatify/smorm/internal/brand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var out, errOut bytes.Buffer
	if run(nil, &out, &errOut) != 2 || run([]string{"bogus"}, &out, &errOut) != 2 {
		t.Fatal("usage errors")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n"), 0o644)
	if code := run([]string{"init", "-dir", dir, "-dialect", "sqlite"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "created:") {
		t.Fatalf("init: %d %s %s", code, out.String(), errOut.String())
	}
	if run([]string{"init", "-dir", dir, "-dialect", "oracle"}, &out, &errOut) != 1 || run([]string{"init", "-x"}, &out, &errOut) != 2 {
		t.Fatal("init errors")
	}
	out.Reset()
	if code := run([]string{"generate", "../../internal/fixture/blog"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "generated:") {
		t.Fatalf("generate: %s", errOut.String())
	}
	if run([]string{"generate", "does-not-exist"}, &out, &errOut) != 1 {
		t.Fatal("generate error")
	}
}

func TestRenameTags(t *testing.T) {
	var out, errOut bytes.Buffer
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "e.go"), []byte("package e\n\ntype E struct {\n\tA string `"+brand.TagKey+":\"length:1\"`\n}\n"), 0o644)
	if code := run([]string{"rename-tags", "-to", "orm", dir}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "1 file(s) updated") {
		t.Fatalf("rename: %d %s %s", code, out.String(), errOut.String())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "e.go"))
	if !strings.Contains(string(b), "`orm:\"length:1\"`") {
		t.Fatalf("file:\n%s", b)
	}
	if run([]string{"rename-tags", "-to", ""}, &out, &errOut) != 1 || run([]string{"rename-tags", "-bogus"}, &out, &errOut) != 2 {
		t.Fatal("errors")
	}
}
