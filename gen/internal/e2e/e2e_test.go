// Package e2e runs the complete developer workflow in a fresh module:
// init, make:entity, make:migration, migrations:migrate, schema:validate
// and an application using the entity manager with lazy loading.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smatify/smorm/gen/scaffold"
	"github.com/smatify/smorm/internal/brand"
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

type project struct {
	t   *testing.T
	dir string
	env []string
}

func (p *project) run(stdin string, name string, args ...string) string {
	p.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = p.dir
	cmd.Env = p.env
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		p.t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func (p *project) console(stdin string, args ...string) string {
	return p.run(stdin, "go", append([]string{"run", "./cmd/console"}, args...)...)
}

const demo = `package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"ROOT"
	"ROOT/dbal"
	"ROOT/dialect/sqlitedialect"

	"example.com/blog/internal/entity"
)

func main() {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", os.Getenv("DSNENV"))
	if err != nil {
		panic(err)
	}
	db := dbal.NewDB(sqlDB, sqlitedialect.New())
	em := NAME.NewEntityManager(db, NAME.WithContext(ctx))

	ann := (&entity.Author{}).SetName("Ann")
	ann.AddPost((&entity.Post{}).SetTitle("Hello"))
	ann.AddPost((&entity.Post{}).SetTitle("World"))
	if err := em.Persist(ann); err != nil {
		panic(err)
	}
	for _, p := range ann.Posts() {
		if err := em.Persist(p); err != nil {
			panic(err)
		}
	}
	if err := em.Flush(ctx); err != nil {
		panic(err)
	}

	em2 := NAME.NewEntityManager(db, NAME.WithContext(ctx))
	posts, err := entity.NewPostRepository(em2).FindBy(ctx, NAME.Criteria{"title": "World"})
	if err != nil {
		panic(err)
	}
	p := posts[0]
	fmt.Printf("post=%s author=%s initialized=%v\n", p.Title(), p.Author().Name(), em2.IsInitialized(p.Author()))
	fmt.Printf("author posts=%d\n", len(p.Author().Posts()))
}
`

func TestWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	root := repoRoot()
	dir := t.TempDir()
	mod := "github.com/smatify/" + brand.Name
	gomod := "module example.com/blog\n\ngo 1.26.0\n\nrequire (\n" +
		"\t" + mod + " v0.0.0\n\t" + mod + "/gen v0.0.0\n\t" + mod + "/dialect/sqlitedialect v0.0.0\n)\n\n" +
		"replace " + mod + " => " + root + "\n" +
		"replace " + mod + "/gen => " + filepath.Join(root, "gen") + "\n" +
		"replace " + mod + "/dialect/sqlitedialect => " + filepath.Join(root, "dialect", "sqlitedialect") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	dsn := "file:" + filepath.Join(dir, "app.db") + "?_pragma=foreign_keys(1)"
	p := &project{t: t, dir: dir, env: append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GONOSUMDB=*",
		brand.Env("database_url")+"="+dsn)}

	if _, err := scaffold.Init(scaffold.Options{Dir: dir, Dialect: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	p.run("", "go", "mod", "tidy")

	// Post with a title and a manyToOne author (inverse: Author.posts).
	p.console(strings.Join([]string{
		"",                             // description
		"no",                           // timestamps
		"title", "", "200", "no", "no", // string(200)
		"author", "relation", "Author", "", "no", "", "", "", "", "no",
		"",
	}, "\n")+"\n", "make:entity", "Post")
	p.console("name\n\n100\nno\nyes\n\n", "make:entity", "Author")

	out := p.console("", "make:migration", "-n")
	if !strings.Contains(out, "Generated new migration") {
		t.Fatalf("make:migration:\n%s", out)
	}
	out = p.console("", "migrations:migrate", "-n")
	if !strings.Contains(out, "Successfully executed 1") {
		t.Fatalf("migrate:\n%s", out)
	}
	if out := p.console("", "schema:validate"); !strings.Contains(out, "in sync") || !strings.Contains(out, "mapping is correct") {
		t.Fatalf("validate:\n%s", out)
	}

	demoSrc := strings.NewReplacer("ROOT", mod, "NAME", brand.Name, "DSNENV", brand.Env("database_url")).Replace(demo)
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "demo", "main.go"), []byte(demoSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out = p.run("", "go", "run", "./cmd/demo")
	if !strings.Contains(out, "post=World author=Ann initialized=true") || !strings.Contains(out, "author posts=2") {
		t.Fatalf("demo:\n%s", out)
	}

	// Evolve the schema: add a field, migrate again.
	p.console("subtitle\n\n\nyes\nno\n\n", "make:entity", "Post")
	if out := p.console("", "make:migration", "-n"); !strings.Contains(out, "Generated new migration") {
		t.Fatalf("second migration:\n%s", out)
	}
	if out := p.console("", "migrations:migrate", "-n"); !strings.Contains(out, "Successfully executed 1") {
		t.Fatalf("second migrate:\n%s", out)
	}
	if out := p.console("", "migrations:status"); !strings.Contains(out, "0 pending") {
		t.Fatalf("status:\n%s", out)
	}
	if out := p.console("", "migrations:migrate", "prev", "-n"); !strings.Contains(out, "-- reverting") {
		t.Fatalf("rollback:\n%s", out)
	}
	p.run("", "go", "vet", "./...")
	if target := os.Getenv("E2E_EXPORT"); target != "" {
		export(t, dir, target, root)
	}
}

// export copies the generated project (without the database) to target and
// makes the replace directives relative to the repository.
func export(t *testing.T, from, to, root string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() || strings.HasPrefix(filepath.Base(path), "app.db") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rel == "go.mod" {
			relRoot, _ := filepath.Rel(to, root)
			b = []byte(strings.ReplaceAll(string(b), root, filepath.ToSlash(relRoot)))
		}
		dst := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
