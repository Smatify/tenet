package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestForwardOnlyAndNotNullWarning(t *testing.T) {
	dir := t.TempDir()
	files, err := Generate(GenerateOptions{Dir: dir, Version: "20260101000000", Up: []string{"SELECT 1"}, Down: []string{"SELECT 2"}, ForwardOnly: true})
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	src, _ := os.ReadFile(files[0])
	if strings.Contains(string(src), "Down") || strings.Contains(string(src), "SELECT 2") {
		t.Fatalf("forward only file has a Down part:\n%s", src)
	}
	if files, _ := Generate(GenerateOptions{Dir: dir, Version: "20260101000001", Format: FormatSQL, Up: []string{"SELECT 1"}, ForwardOnly: true}); len(files) != 1 {
		t.Fatalf("sql forward only: %v", files)
	}
	got := NotNullWithoutDefault([]string{
		`ALTER TABLE "t" ADD COLUMN "a" BOOLEAN NOT NULL`,
		`ALTER TABLE "t" ADD COLUMN "b" BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE "t" ADD COLUMN "c" VARCHAR(5) NULL`,
		`CREATE TABLE "u" ("id" INT NOT NULL)`,
	})
	if len(got) != 1 || got[0] != `"t"."a"` {
		t.Fatalf("NotNullWithoutDefault = %v", got)
	}
}
