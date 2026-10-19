package maker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smatify/smorm/console"
	"github.com/smatify/smorm/gen"
)

const valueCheck = `package main

import (
	"fmt"

	"ROOT/mapping"
	"example.com/app/entity"
	_ "example.com/app/fixtures"
)

func main() {
	e, err := mapping.For[entity.Customer](mapping.NewRegistry())
	if err != nil {
		panic(err)
	}
	fmt.Println(e.Columns())
}
`

func TestEmbeddableTraitAndFixtures(t *testing.T) {
	dir := tempModule(t)
	entDir := filepath.Join(dir, "entity")
	mk := &Maker{Dir: entDir}
	if _, err := mk.ApplyValue(KindEmbeddable, "Address", []FieldSpec{{Name: "street", Type: "string", Length: 100}, {Name: "city", Type: "string"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mk.ApplyValue(KindTrait, "Audited", []FieldSpec{{Name: "auditedBy", Type: "string", Nullable: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mk.Apply(EntitySpec{Name: "Customer", Fields: []FieldSpec{
		{Name: "billing", Type: "embedded", Embeddable: "Address"},
		{Name: "shipping", Type: "embedded", Embeddable: "Address", Prefix: "ship_"},
		{Name: "home", Type: "embedded", Embeddable: "Address", Prefix: "-"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeFixtures(filepath.Join(dir, "fixtures"), "AppFixtures"); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeFixtures(filepath.Join(dir, "fixtures"), "AppFixtures"); err == nil {
		t.Fatal("existing fixture file")
	}
	if _, err := gen.Generate(entDir); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "check"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "check", "main.go"), []byte(strings.ReplaceAll(valueCheck, "ROOT", importPath())), 0o644)
	goCmd(t, dir, "vet", "./...")
	out := goCmd(t, dir, "run", "./check")
	if !strings.Contains(out, "billing_street billing_city ship_street ship_city street city") {
		t.Fatalf("columns: %s", out)
	}
	// Errors.
	if _, err := mk.ApplyValue(KindEmbeddable, "lower", nil); err == nil {
		t.Fatal("invalid name")
	}
	if _, err := mk.ApplyValue(KindEmbeddable, "Address", []FieldSpec{{Name: "x", Type: "relation"}}); err == nil {
		t.Fatal("relation in embeddable")
	}
	if _, err := mk.Apply(EntitySpec{Name: "Customer", Fields: []FieldSpec{{Name: "other", Type: "embedded", Embeddable: "Missing"}}}); err == nil {
		t.Fatal("missing embeddable")
	}
	if _, err := MakeFixtures(dir, "1bad"); err == nil {
		t.Fatal("invalid fixture name")
	}
}

func TestValueCommands(t *testing.T) {
	dir := tempModule(t)
	t.Chdir(dir)
	_ = os.MkdirAll("cmd/console", 0o755)
	_ = os.WriteFile("cmd/console/main.go", []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println() }\n"), 0o644)
	app := console.New(console.Config{EntityDir: "entity"})
	Register(app)
	var out bytes.Buffer
	app.Out, app.Err = &out, &out
	app.In = strings.NewReader("Geo\nlat\nfloat\nno\n\n")
	if code := app.Run(context.Background(), []string{"make:embeddable"}); code != 0 {
		t.Fatal(out.String())
	}
	if src, _ := os.ReadFile("entity/geo.go"); !strings.Contains(string(src), "lat float64") {
		t.Fatalf("geo.go:\n%s", src)
	}
	out.Reset()
	if code := app.Run(context.Background(), []string{"make:trait", "Stamped", "-n"}); code != 0 {
		t.Fatal(out.String())
	}
	if code := app.Run(context.Background(), []string{"make:fixtures", "Demo"}); code != 0 || !strings.Contains(out.String(), "updated: cmd/console/main.go") {
		t.Fatal(out.String())
	}
	if src, _ := os.ReadFile("cmd/console/main.go"); !strings.Contains(string(src), `_ "example.com/app/fixtures"`) {
		t.Fatalf("main.go:\n%s", src)
	}
	if added, err := EnsureBlankImport("cmd/console/main.go", "example.com/app/fixtures"); err != nil || added {
		t.Fatal("import must not be added twice")
	}
	app.Config.EntityDir = ""
	if code := app.Run(context.Background(), []string{"make:embeddable", "X"}); code != 1 {
		t.Fatal("missing entity dir")
	}
}
