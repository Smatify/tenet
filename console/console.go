// Package console is the command framework behind the project console
// (the equivalent of Symfony's bin/console). Applications build it in
// cmd/console/main.go; the maker adds make:* commands.
package console

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect"
	"github.com/smatify/tenet/fixtures"
	"github.com/smatify/tenet/internal/brand"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/migrations"
)

// Command is a console command.
type Command struct {
	Name        string // e.g. "migrations:migrate"
	Usage       string // argument synopsis, e.g. "[version]"
	Description string
	Flags       func(fs *flag.FlagSet)
	Run         func(ctx context.Context, c *Ctx) error
}

// Config configures the console of an application.
type Config struct {
	// DB is used when set; otherwise Driver and DSN open the database.
	DB      *dbal.DB
	Driver  string
	DSN     string // default: $<PREFIX>DATABASE_URL
	Dialect dialect.Dialect

	// EntityDir is the directory of the entity package (for the maker),
	// EntityImport its import path.
	EntityDir    string
	EntityImport string
	// Entities lists entity values explicitly; by default every entity with
	// generated code linked into the binary is used.
	Entities []any
	Registry *mapping.Registry

	MigrationsDir    string            // default "migrations"
	MigrationsFormat migrations.Format // default go
	// MigrationsForwardOnly generates migrations without a Down part.
	MigrationsForwardOnly bool
	Migrations            *migrations.Registry

	// IgnoreTables excludes tables from diffs and validation.
	IgnoreTables func(table string) bool

	// Fixtures defaults to fixtures.Default.
	Fixtures *fixtures.Registry
}

// App is a console application.
type App struct {
	Name     string
	Config   Config
	In       io.Reader
	Out, Err io.Writer
	commands map[string]Command
	db       *dbal.DB
}

// New returns an app with the built-in migration and schema commands.
func New(cfg Config) *App {
	if cfg.MigrationsDir == "" {
		cfg.MigrationsDir = "migrations"
	}
	if cfg.MigrationsFormat == "" {
		cfg.MigrationsFormat = migrations.FormatGo
	}
	if cfg.Migrations == nil {
		cfg.Migrations = migrations.Default
	}
	if cfg.Registry == nil {
		cfg.Registry = mapping.Default
	}
	if cfg.Fixtures == nil {
		cfg.Fixtures = fixtures.Default
	}
	if cfg.DSN == "" {
		cfg.DSN = os.Getenv(brand.Env("database_url"))
	}
	a := &App{Name: brand.Name, Config: cfg, In: os.Stdin, Out: os.Stdout, Err: os.Stderr, commands: map[string]Command{}}
	a.Add(builtinCommands()...)
	return a
}

// Add registers commands (replacing commands with the same name).
func (a *App) Add(cmds ...Command) {
	for _, c := range cmds {
		a.commands[c.Name] = c
	}
}

// Commands returns the registered commands sorted by name.
func (a *App) Commands() []Command {
	out := make([]Command, 0, len(a.commands))
	for _, c := range a.commands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DB opens (once) and returns the database.
func (a *App) DB() (*dbal.DB, error) {
	if a.db != nil {
		return a.db, nil
	}
	if a.Config.DB != nil {
		a.db = a.Config.DB
		return a.db, nil
	}
	if a.Config.Driver == "" || a.Config.DSN == "" || a.Config.Dialect == nil {
		return nil, fmt.Errorf("console: no database configured (set Driver, Dialect and %s)", brand.Env("database_url"))
	}
	sqlDB, err := sql.Open(a.Config.Driver, a.Config.DSN)
	if err != nil {
		return nil, err
	}
	a.db = dbal.NewDB(sqlDB, a.Config.Dialect)
	return a.db, nil
}

// Entities returns the metadata of all entities.
func (a *App) Entities() ([]*mapping.Entity, error) {
	var out []*mapping.Entity
	add := func(t reflect.Type) error {
		e, err := a.Config.Registry.Get(t)
		if err != nil {
			return err
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
		return nil
	}
	if len(a.Config.Entities) > 0 {
		for _, v := range a.Config.Entities {
			if err := add(reflect.TypeOf(v)); err != nil {
				return nil, err
			}
		}
	} else {
		for _, t := range mapping.GeneratedEntities() {
			if err := add(t); err != nil {
				return nil, err
			}
		}
	}
	// Include association targets so the schema is complete.
	for i := 0; i < len(out); i++ {
		for _, as := range out[i].Assocs {
			if !slices.Contains(out, as.Target) {
				out = append(out, as.Target)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Migrator returns a migrator for the configured database.
func (a *App) Migrator() (*migrations.Migrator, error) {
	db, err := a.DB()
	if err != nil {
		return nil, err
	}
	return migrations.New(db, a.Config.Migrations), nil
}

// Run executes the command named by args[0] and returns the exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "list" || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) > 1 {
			return a.help(args[1])
		}
		a.list()
		return 0
	}
	cmd, ok := a.commands[args[0]]
	if !ok {
		fmt.Fprintf(a.Err, "Command %q is not defined.\n", args[0])
		if alts := a.suggest(args[0]); len(alts) > 0 {
			fmt.Fprintf(a.Err, "Did you mean one of these?\n    %s\n", strings.Join(alts, "\n    "))
		}
		return 1
	}
	fs := flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	noInteraction := fs.Bool("no-interaction", false, "do not ask any interactive question")
	fs.BoolVar(noInteraction, "n", false, "shorthand for --no-interaction")
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	positional, err := parseInterleaved(fs, args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	c := &Ctx{App: a, Flags: fs, Args: positional, Out: a.Out, Err: a.Err, Interactive: !*noInteraction,
		in: bufio.NewReader(a.In)}
	if err := cmd.Run(ctx, c); err != nil {
		fmt.Fprintf(a.Err, "\n [ERROR] %v\n\n", err)
		return 1
	}
	return 0
}

// parseInterleaved allows flags after positional arguments.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func (a *App) list() {
	fmt.Fprintf(a.Out, "%s console\n\nUsage:\n  command [options] [arguments]\n\nAvailable commands:\n", a.Name)
	group := ""
	for _, c := range a.Commands() {
		g, _, found := strings.Cut(c.Name, ":")
		if found && g != group {
			fmt.Fprintf(a.Out, " %s\n", g)
			group = g
		}
		fmt.Fprintf(a.Out, "  %-28s %s\n", c.Name, c.Description)
	}
}

func (a *App) help(name string) int {
	cmd, ok := a.commands[name]
	if !ok {
		fmt.Fprintf(a.Err, "Command %q is not defined.\n", name)
		return 1
	}
	fmt.Fprintf(a.Out, "Description:\n  %s\n\nUsage:\n  %s %s\n", cmd.Description, cmd.Name, cmd.Usage)
	fs := flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	fmt.Fprintln(a.Out, "\nOptions:")
	fs.SetOutput(a.Out)
	fs.PrintDefaults()
	fmt.Fprintln(a.Out, "  -n, -no-interaction\n    \tdo not ask any interactive question")
	return 0
}

func (a *App) suggest(name string) []string {
	var out []string
	for _, c := range a.Commands() {
		if strings.Contains(c.Name, name) || strings.HasPrefix(c.Name, strings.SplitN(name, ":", 2)[0]+":") {
			out = append(out, c.Name)
		}
	}
	return out
}

// Ctx is passed to running commands.
type Ctx struct {
	App         *App
	Flags       *flag.FlagSet
	Args        []string
	Out, Err    io.Writer
	Interactive bool
	in          *bufio.Reader
}

// Arg returns positional argument i or "".
func (c *Ctx) Arg(i int) string {
	if i < len(c.Args) {
		return c.Args[i]
	}
	return ""
}

// Printf writes to the output.
func (c *Ctx) Printf(format string, args ...any) { fmt.Fprintf(c.Out, format, args...) }

// Success prints a green-box style success line.
func (c *Ctx) Success(msg string) { fmt.Fprintf(c.Out, "\n [OK] %s\n\n", msg) }

// Ask prompts for a line of input; def is returned for empty input or when
// not interactive.
func (c *Ctx) Ask(question, def string) string {
	if !c.Interactive {
		return def
	}
	if def != "" {
		fmt.Fprintf(c.Out, "\n %s [%s]:\n > ", question, def)
	} else {
		fmt.Fprintf(c.Out, "\n %s:\n > ", question)
	}
	line, err := c.in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" || (err != nil && line == "") {
		return def
	}
	return line
}

// Confirm asks a yes/no question.
func (c *Ctx) Confirm(question string, def bool) bool {
	d := "no"
	if def {
		d = "yes"
	}
	switch strings.ToLower(c.Ask(question+" (yes/no)", d)) {
	case "y", "yes", "j", "ja":
		return true
	case "n", "no", "nein":
		return false
	}
	return def
}

// Choice asks to pick one of options (by value or number).
func (c *Ctx) Choice(question string, options []string, def string) string {
	if !c.Interactive {
		return def
	}
	for {
		var b strings.Builder
		for i, o := range options {
			fmt.Fprintf(&b, "\n  [%d] %s", i, o)
		}
		ans := c.Ask(question+b.String(), def)
		for i, o := range options {
			if strings.EqualFold(ans, o) || ans == fmt.Sprint(i) {
				return o
			}
		}
		if ans == "" {
			return def
		}
		fmt.Fprintf(c.Out, " [ERROR] Value %q is invalid\n", ans)
		if _, err := c.in.Peek(1); err != nil {
			return def // no more input
		}
	}
}
