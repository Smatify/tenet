package maker

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/smatify/tenet/console"
)

func TestRegisterAndGenerate(t *testing.T) {
	app := console.New(console.Config{EntityDir: "../internal/fixture/blog"})
	var out, errOut bytes.Buffer
	app.Out, app.Err = &out, &errOut
	Register(app)
	names := []string{}
	for _, c := range app.Commands() {
		names = append(names, c.Name)
	}
	for _, want := range []string{"make:entity", "make:migration", "make:generate"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	if code := app.Run(context.Background(), []string{"make:generate"}); code != 0 || !strings.Contains(out.String(), "up to date") {
		t.Fatalf("generate: %s%s", out.String(), errOut.String())
	}
	app.Config.EntityDir = ""
	if code := app.Run(context.Background(), []string{"make:generate"}); code != 1 {
		t.Fatal("missing entity dir")
	}
	if code := app.Run(context.Background(), []string{"make:generate", "nope"}); code != 1 {
		t.Fatal("bad dir")
	}
}
