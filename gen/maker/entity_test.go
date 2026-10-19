package maker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smatify/smorm/console"
)

func runMaker(t *testing.T, entDir, input string, args ...string) (string, int) {
	t.Helper()
	app := console.New(console.Config{EntityDir: entDir})
	Register(app)
	var out bytes.Buffer
	app.In, app.Out, app.Err = strings.NewReader(input), &out, &out
	code := app.Run(context.Background(), append([]string{"make:entity"}, args...))
	return out.String(), code
}

func TestMakeEntityWizard(t *testing.T) {
	dir := tempModule(t)
	entDir := filepath.Join(dir, "entity")
	input := strings.Join([]string{
		"yes",                          // timestamps
		"title", "", "200", "no", "no", // string(200)
		"publishedAt", "", "yes", "no", // datetime guessed, nullable
		"isActive", "", "no", "no", // boolean guessed
		"price", "decimal", "8", "2", "no", "no",
		"1x", "string", "", "no", "no", // invalid name: error, loop continues
		"author", "relation", "Author", "", "no", "", "", "no", // manyToOne, not null, inverse posts, no orphanRemoval
		"tags", "relation", "Tag", "2", "", "", // manyToMany with inverse
		"",
	}, "\n") + "\n"
	out, code := runMaker(t, entDir, input, "Post")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, want := range []string{"created:", "[ERROR]", "Success!", "make:migration"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	goCmd(t, dir, "vet", "./...")
	post, _ := os.ReadFile(filepath.Join(entDir, "post.go"))
	for _, want := range []string{"Timestamps", "title string", `length:200`, "publishedAt *time.Time", "isActive bool",
		"type:decimal,precision:8,scale:2", "author *Author", "manyToOne,inversedBy:posts,notnull", "tags " + root + ".Collection[Tag]"} {
		if !strings.Contains(norm(string(post)), norm(want)) {
			t.Errorf("post.go misses %q:\n%s", want, post)
		}
	}
	if _, err := os.Stat(filepath.Join(entDir, "entities_smorm.go")); err != nil {
		t.Fatal("generated code missing")
	}
	// Running again on an existing entity adds fields.
	out, code = runMaker(t, entDir, "subtitle\n\n\nyes\nno\n\n", "post")
	if code != 0 || !strings.Contains(out, "already exists") {
		t.Fatalf("existing: %d\n%s", code, out)
	}
	goCmd(t, dir, "vet", "./...")
	// Non-interactive creates the skeleton only.
	if out, code := runMaker(t, entDir, "", "Plain", "-n", "-uuid", "-style", "go"); code != 0 {
		t.Fatalf("non-interactive: %s", out)
	}
	if _, err := os.Stat(filepath.Join(entDir, "plain.go")); err != nil {
		t.Fatal("skeleton not created")
	}
}

func TestMakeEntityErrors(t *testing.T) {
	if _, code := runMaker(t, "", "", "Post"); code != 1 {
		t.Fatal("missing entity dir")
	}
	dir := t.TempDir()
	if _, code := runMaker(t, dir, "", "Post", "-style", "weird"); code != 1 {
		t.Fatal("bad style")
	}
	if _, code := runMaker(t, dir, "\n"); code != 1 {
		t.Fatal("missing name")
	}
	if _, code := runMaker(t, dir, "", "lower case", "-n"); code != 1 {
		t.Fatal("invalid name")
	}
	if guessType("createdAt") != "datetime" || guessType("hasPaid") != "boolean" || guessType("birthdate") != "date" || guessType("uuid") != "uuid" || guessType("name") != "string" {
		t.Fatal("guessType")
	}
}
