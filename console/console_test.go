package console

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/internal/fakedb"
	"github.com/smatify/smorm/internal/testdialect"
	"github.com/smatify/smorm/migrations"
)

func testApp(input string) (*App, *bytes.Buffer, *bytes.Buffer) {
	db := dbal.NewDB(fakedb.New().DB(), testdialect.New())
	a := New(Config{DB: db, Migrations: migrations.NewRegistry()})
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	a.In, a.Out, a.Err = strings.NewReader(input), out, errOut
	return a, out, errOut
}

func TestListHelpAndUnknown(t *testing.T) {
	a, out, errOut := testApp("")
	if code := a.Run(context.Background(), nil); code != 0 || !strings.Contains(out.String(), "migrations:migrate") || !strings.Contains(out.String(), " schema\n") {
		t.Fatalf("list:\n%s", out)
	}
	out.Reset()
	if code := a.Run(context.Background(), []string{"help", "migrations:execute"}); code != 0 || !strings.Contains(out.String(), "-down") || !strings.Contains(out.String(), "no-interaction") {
		t.Fatalf("help:\n%s", out)
	}
	if code := a.Run(context.Background(), []string{"help", "nope"}); code != 1 {
		t.Fatal("help unknown")
	}
	if code := a.Run(context.Background(), []string{"migrations:migrat"}); code != 1 || !strings.Contains(errOut.String(), "Did you mean") {
		t.Fatalf("suggest:\n%s", errOut)
	}
	if code := a.Run(context.Background(), []string{"migrations:status", "--bogus"}); code != 2 {
		t.Fatal("bad flag")
	}
	if code := a.Run(context.Background(), []string{"migrations:status", "-h"}); code != 0 {
		t.Fatal("-h")
	}
}

func TestCommandFlowAndFlags(t *testing.T) {
	a, out, errOut := testApp("")
	var got []string
	var name *string
	a.Add(Command{
		Name:  "demo:run",
		Flags: func(fs *flag.FlagSet) { name = fs.String("name", "x", "") },
		Run: func(_ context.Context, c *Ctx) error {
			got = append(got, c.Arg(0), c.Arg(1), c.Arg(5), *name)
			if !c.Interactive {
				got = append(got, "non-interactive")
			}
			c.Printf("hi\n")
			return nil
		},
	})
	if code := a.Run(context.Background(), []string{"demo:run", "a", "--name=y", "b", "-n"}); code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	if strings.Join(got, ",") != "a,b,,y,non-interactive" || out.String() != "hi\n" {
		t.Fatalf("got %v out %q", got, out)
	}
	a.Add(Command{Name: "demo:fail", Run: func(context.Context, *Ctx) error { return context.Canceled }})
	if code := a.Run(context.Background(), []string{"demo:fail"}); code != 1 || !strings.Contains(errOut.String(), "[ERROR]") {
		t.Fatal("error output")
	}
}

func TestPrompts(t *testing.T) {
	a, out, _ := testApp("Post\n\nyes\nmaybe\n2\nbogus\n")
	var answers []string
	a.Add(Command{Name: "demo:ask", Run: func(_ context.Context, c *Ctx) error {
		answers = append(answers, c.Ask("Class name", ""), c.Ask("Table", "post"))
		if c.Confirm("Sure?", false) {
			answers = append(answers, "yes")
		}
		if !c.Confirm("Again?", false) {
			answers = append(answers, "default-no")
		}
		answers = append(answers, c.Choice("Type", []string{"string", "int", "bool"}, "string"))
		answers = append(answers, c.Choice("Type", []string{"string", "int"}, "int"))
		c.Success("done")
		return nil
	}})
	if code := a.Run(context.Background(), []string{"demo:ask"}); code != 0 {
		t.Fatal(out)
	}
	if strings.Join(answers, ",") != "Post,post,yes,default-no,bool,int" {
		t.Fatalf("answers = %v\n%s", answers, out)
	}
	if !strings.Contains(out.String(), "[0] string") || !strings.Contains(out.String(), "[OK] done") || !strings.Contains(out.String(), "invalid") {
		t.Fatalf("output:\n%s", out)
	}
	b, _, _ := testApp("")
	var nonInteractive []string
	b.Add(Command{Name: "demo:ni", Run: func(_ context.Context, c *Ctx) error {
		nonInteractive = append(nonInteractive, c.Ask("q", "d"), c.Choice("q", []string{"a"}, "a"))
		return nil
	}})
	b.Run(context.Background(), []string{"demo:ni", "-n"})
	if strings.Join(nonInteractive, ",") != "d,a" {
		t.Fatal("non-interactive defaults")
	}
}

func TestStatusWithFakeDB(t *testing.T) {
	a, out, errOut := testApp("")
	reg := migrations.NewRegistry()
	_ = reg.Add(migrations.Migration{Version: "001", Description: "first", Up: func(context.Context, *migrations.M) error { return nil }})
	a.Config.Migrations = reg
	if code := a.Run(context.Background(), []string{"migrations:status"}); code != 0 {
		t.Fatal(errOut)
	}
	if !strings.Contains(out.String(), "001") || !strings.Contains(out.String(), "not migrated") || !strings.Contains(out.String(), "1 pending") {
		t.Fatalf("status:\n%s", out)
	}
}

func TestDBConfiguration(t *testing.T) {
	a := New(Config{})
	if _, err := a.DB(); err == nil {
		t.Fatal("missing configuration must fail")
	}
	if a.Config.MigrationsDir != "migrations" || a.Config.MigrationsFormat != migrations.FormatGo {
		t.Fatal("defaults")
	}
	b := New(Config{Driver: "nope", DSN: "x", Dialect: testdialect.New()})
	if _, err := b.DB(); err == nil {
		t.Fatal("unknown driver must fail")
	}
	if _, err := b.Migrator(); err == nil {
		t.Fatal("migrator without db")
	}
	if a.format("sql") != migrations.FormatSQL || a.format("") != migrations.FormatGo {
		t.Fatal("format")
	}
	if code := a.Run(context.Background(), []string{"migrations:status"}); code != 1 {
		t.Fatal("status without db")
	}
}
