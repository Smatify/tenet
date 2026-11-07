package maker

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smatify/smorm/gen"
)

// tempModule creates a module that uses the local core module.
func tempModule(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	core := filepath.Join(filepath.Dir(file), "..", "..")
	dir := t.TempDir()
	gomod := "module example.com/app\n\ngo 1.26.0\n\nrequire " + importPath() + " v0.0.0\n\nreplace " + importPath() + " => " + core + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func goCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
	return string(out)
}

const checkProgram = `package main

import (
	"fmt"
	"os"

	"%[1]s/mapping"
	_ "example.com/app/entity"
)

func main() {
	r := mapping.NewRegistry()
	for _, t := range mapping.GeneratedEntities() {
		e, err := r.Get(t)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Printf("%s:%s:%d:%d\n", e.Name, e.Table, len(e.Fields), len(e.Assocs))
	}
}
`

func TestApplyDoctrineStyleCompilesAndMaps(t *testing.T) {
	dir := tempModule(t)
	entDir := filepath.Join(dir, "entity")
	mk := &Maker{Dir: entDir}
	_, err := mk.Apply(EntitySpec{Name: "Post", Timestamps: true, Fields: []FieldSpec{
		{Name: "title", Type: "string", Length: 200},
		{Name: "body", Type: "text", Nullable: true},
		{Name: "slug", Type: "string", Unique: true},
		{Name: "views", Type: "integer"},
		{Name: "rating", Type: "float", Nullable: true},
		{Name: "price", Type: "decimal"},
		{Name: "publishedAt", Type: "datetime", Nullable: true},
		{Name: "day", Type: "date"},
		{Name: "meta", Type: "json", Nullable: true},
		{Name: "ref", Type: "uuid"},
		{Name: "blob", Type: "binary", Nullable: true},
		{Name: "author", Type: "relation", Relation: ManyToOne, Target: "Author", Inverse: "posts"},
		{Name: "tags", Type: "relation", Relation: ManyToMany, Target: "Tag", Inverse: "posts"},
		{Name: "cover", Type: "relation", Relation: OneToOne, Target: "Image", Inverse: "post", Nullable: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Extend an existing entity: OneToMany creates the owning side on the target.
	_, err = mk.Apply(EntitySpec{Name: "Author", Fields: []FieldSpec{
		{Name: "name", Type: "string"},
		{Name: "comments", Type: "relation", Relation: OneToMany, Target: "Comment", Inverse: "author", OrphanRemoval: true},
		{Name: "parent", Type: "relation", Relation: ManyToOne, Target: "Author", Nullable: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(entDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "check"), 0o755); err != nil {
		t.Fatal(err)
	}
	prog := strings.ReplaceAll(checkProgram, "%[1]s", importPath())
	if err := os.WriteFile(filepath.Join(dir, "check", "main.go"), []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	goCmd(t, dir, "vet", "./...")
	out := goCmd(t, dir, "run", "./check")
	for _, want := range []string{"Author:author:", "Comment:comment:", "Image:image:", "Post:post:", "Tag:tag:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	post, _ := os.ReadFile(filepath.Join(entDir, "post.go"))
	for _, want := range []string{"func (p *Post) Title() string", "func (p *Post) SetAuthor(v *Author) *Post", "func (p *Post) AddTag(item *Tag) *Post",
		"publishedAt *time.Time", "title       string `" + "smorm" + ":\"length:200\"`"} {
		if !strings.Contains(norm(string(post)), norm(want)) {
			t.Errorf("post.go misses %q:\n%s", want, post)
		}
	}
	author, _ := os.ReadFile(filepath.Join(entDir, "author.go"))
	for _, want := range []string{"func (a *Author) AddPost(item *Post) *Author", "item.SetAuthor(a)", "func (a *Author) RemoveComment(item *Comment)"} {
		if !strings.Contains(string(author), want) {
			t.Errorf("author.go misses %q:\n%s", want, author)
		}
	}
	if _, err := os.Stat(filepath.Join(entDir, "post_repository.go")); err != nil {
		t.Fatal("repository not created")
	}
	// Adding an existing property fails.
	if _, err := mk.Apply(EntitySpec{Name: "Post", Fields: []FieldSpec{{Name: "title", Type: "string"}}}); err == nil {
		t.Fatal("duplicate property")
	}
}

func TestApplyGoStyle(t *testing.T) {
	dir := tempModule(t)
	entDir := filepath.Join(dir, "entity")
	mk := &Maker{Dir: entDir, Style: StyleGo}
	_, err := mk.Apply(EntitySpec{Name: "Order", UUID: true, Fields: []FieldSpec{
		{Name: "number", Type: "string", Length: 20, Unique: true},
		{Name: "customer", Type: "relation", Relation: ManyToOne, Target: "Customer", Inverse: "orders"},
		{Name: "items", Type: "relation", Relation: ManyToMany, Target: "Product", Inverse: "orders"},
		{Name: "invoice", Type: "relation", Relation: OneToOne, Target: "Invoice", Inverse: "order", Nullable: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(entDir); err != nil {
		t.Fatal(err)
	}
	goCmd(t, dir, "vet", "./...")
	src, _ := os.ReadFile(filepath.Join(entDir, "order.go"))
	for _, want := range []string{"ID       uuid.UUID", "Customer " + "smorm" + ".Ref[Customer]", "Number   string"} {
		if !strings.Contains(norm(string(src)), norm(want)) {
			t.Errorf("order.go misses %q:\n%s", want, src)
		}
	}
	if strings.Contains(string(src), "func (") {
		t.Fatal("go style must not generate methods")
	}
}

func TestApplyErrors(t *testing.T) {
	dir := t.TempDir()
	mk := &Maker{Dir: dir}
	cases := []EntitySpec{
		{Name: "lower"},
		{Name: "Bad Name"},
		{Name: "Post", Fields: []FieldSpec{{Name: "x", Type: "nope"}}},
		{Name: "Post", Fields: []FieldSpec{{Name: "1x", Type: "string"}}},
		{Name: "Post", Fields: []FieldSpec{{Name: "a", Type: "relation", Relation: "weird", Target: "B"}}},
		{Name: "Post", Fields: []FieldSpec{{Name: "a", Type: "relation", Relation: ManyToOne, Target: "lower"}}},
	}
	for i, c := range cases {
		if _, err := mk.Apply(c); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "thing.go"), []byte("package x\n\ntype Other struct{}\n"), 0o644)
	if _, err := mk.Apply(EntitySpec{Name: "Thing"}); err == nil {
		t.Fatal("file without struct")
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{"posts": "post", "heroes": "hero", "halves": "half", "shelves": "shelf", "valves": "valve", "vetoes": "veto", "shoes": "shoe", "toes": "toe", "categories": "category", "boxes": "box", "classes": "class", "news": "new", "data": "data", "address": "address",
		"aliases": "alias", "statuses": "status", "bonuses": "bonus", "viruses": "virus", "houses": "house", "responses": "response"} {
		if got := singular(in); got != want {
			t.Errorf("singular(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"post": "posts", "category": "categories", "box": "boxes", "day": "days", "church": "churches"} {
		if got := plural(in); got != want {
			t.Errorf("plural(%q) = %q", in, got)
		}
	}
	if lower("URL") != "url" || upper("id") != "ID" || upper("name") != "Name" {
		t.Fatal("case helpers")
	}
}

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestApplyRejectsKeywords(t *testing.T) {
	mk := &Maker{Dir: t.TempDir()}
	_, err := mk.Apply(EntitySpec{Name: "Ticket", Fields: []FieldSpec{{Name: "type", Type: "string"}}})
	if err == nil || !strings.Contains(err.Error(), "Go keyword") {
		t.Fatalf("want a keyword error, got %v", err)
	}
	_, err = mk.Apply(EntitySpec{Name: "Ticket", Fields: []FieldSpec{{Name: "owner", Type: "relation", Relation: ManyToOne, Target: "User", Inverse: "func"}}})
	if err == nil || !strings.Contains(err.Error(), "Go keyword") {
		t.Fatalf("want a keyword error for the inverse side, got %v", err)
	}
}

func TestFilesNamedAfterTables(t *testing.T) {
	dir := filepath.Join(tempModule(t), "entity")
	mk := &Maker{Dir: dir}
	if _, err := mk.Apply(EntitySpec{Name: "Demo", Table: "media_demo", Fields: []FieldSpec{{Name: "title", Type: "string"}}}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"media_demo.go", "media_demo_repository.go"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	// A renamed file is found by its struct and extended in place.
	if err := os.Rename(filepath.Join(dir, "media_demo.go"), filepath.Join(dir, "media_replay.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := mk.Apply(EntitySpec{Name: "Demo", Fields: []FieldSpec{{Name: "size", Type: "integer"}}}); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "media_replay.go"))
	if !strings.Contains(string(src), "func (d *Demo) Size()") {
		t.Fatalf("field not added to the renamed file:\n%s", src)
	}
	for _, f := range []string{"demo.go", "media_demo.go"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("%s was created again", f)
		}
	}
}

func TestApplyRelationOptions(t *testing.T) {
	dir := tempModule(t)
	entDir := filepath.Join(dir, "entity")
	mk := &Maker{Dir: entDir}
	_, err := mk.Apply(EntitySpec{Name: "Team", Description: "is a group of players.", Fields: []FieldSpec{
		{Name: "players", Type: "relation", Relation: OneToMany, Target: "Player", Inverse: "team",
			Cascade: []string{"persist", "remove"}, OrphanRemoval: true, OnDelete: "cascade"},
		{Name: "labels", Type: "relation", Relation: ManyToMany, Target: "Label", JoinTable: "team_label"},
		{Name: "captain", Type: "relation", Relation: ManyToOne, Target: "Player", Nullable: true, OnDelete: "set_null"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(entDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "check"), 0o755); err != nil {
		t.Fatal(err)
	}
	prog := strings.ReplaceAll(checkProgram, "%[1]s", importPath())
	if err := os.WriteFile(filepath.Join(dir, "check", "main.go"), []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	goCmd(t, dir, "vet", "./...")
	goCmd(t, dir, "run", "./check")
	team, _ := os.ReadFile(filepath.Join(entDir, "team.go"))
	player, _ := os.ReadFile(filepath.Join(entDir, "player.go"))
	for src, wants := range map[string][]string{string(team): {
		"// Team is a group of players.",
		`smorm:"oneToMany,mappedBy:team,cascade:persist|remove,orphanRemoval"`,
		`smorm:"manyToMany,joinTable:team_label"`,
		`smorm:"manyToOne,onDelete:set_null"`,
		"func (t *Team) SetPlayers(items []*Player) *Team",
		"func (t *Team) SetLabels(items []*Label) *Team",
	}, string(player): {
		`smorm:"manyToOne,inversedBy:players,notnull,onDelete:cascade"`,
	}} {
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Errorf("missing %q in\n%s", want, src)
			}
		}
	}
}
