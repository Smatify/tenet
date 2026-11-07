package migrations

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/smatify/tenet/internal/brand"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/schema"
)

// Format is the file format of generated migrations.
type Format string

// Formats.
const (
	FormatGo  Format = "go"
	FormatSQL Format = "sql"
)

// NewVersion returns a timestamp version (UTC, YYYYMMDDHHMMSS).
func NewVersion(now time.Time) string { return now.UTC().Format("20060102150405") }

// GenerateOptions configures Generate.
type GenerateOptions struct {
	Dir         string // output directory
	Package     string // Go package name (default "migrations")
	Format      Format // default FormatGo
	Version     string // default NewVersion(time.Now())
	Description string
	Up, Down    []string
	// ForwardOnly writes no Down part (projects that undo changes with a
	// new migration).
	ForwardOnly bool
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "_"), "_")
}

// needsNoTx reports whether statements must run outside a transaction
// (SQLite table rebuilds toggle foreign keys, which is ignored in a tx).
func needsNoTx(stmts []string) bool {
	for _, s := range stmts {
		if strings.HasPrefix(strings.ToUpper(s), "PRAGMA FOREIGN_KEYS") {
			return true
		}
	}
	return false
}

// Generate writes a new migration file (or two for SQL) and returns the paths.
func Generate(o GenerateOptions) ([]string, error) {
	if o.Version == "" {
		o.Version = NewVersion(time.Now())
	}
	if o.Package == "" {
		o.Package = "migrations"
	}
	if o.Format == "" {
		o.Format = FormatGo
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, err
	}
	base := "Version" + o.Version
	if s := slug(o.Description); s != "" {
		base = o.Version + "_" + s
	}
	noTx := needsNoTx(o.Up) || needsNoTx(o.Down)
	switch o.Format {
	case FormatSQL:
		if s := slug(o.Description); s == "" {
			base = o.Version
		}
		up := filepath.Join(o.Dir, base+".up.sql")
		down := filepath.Join(o.Dir, base+".down.sql")
		header := ""
		if noTx {
			header = NoTxMarker + "\n"
		}
		if err := os.WriteFile(up, []byte(header+joinSQL(o.Up)), 0o644); err != nil {
			return nil, err
		}
		if o.ForwardOnly {
			return []string{up}, nil
		}
		if err := os.WriteFile(down, []byte(joinSQL(o.Down)), 0o644); err != nil {
			return nil, err
		}
		return []string{up, down}, nil
	case FormatGo:
		var buf bytes.Buffer
		err := goTmpl.Execute(&buf, map[string]any{
			"Package": o.Package, "Version": o.Version, "Description": strconv.Quote(o.Description),
			"Up": quoteAll(o.Up), "Down": quoteAll(o.Down), "NoTx": noTx, "Header": brand.GeneratedHeader, "ForwardOnly": o.ForwardOnly,
		})
		if err != nil {
			return nil, err
		}
		src, err := format.Source(buf.Bytes())
		if err != nil {
			return nil, fmt.Errorf("migrations: formatting generated file: %w", err)
		}
		file := filepath.Join(o.Dir, strings.ToLower(base)+".go")
		return []string{file}, os.WriteFile(file, src, 0o644)
	}
	return nil, fmt.Errorf("migrations: unknown format %q", o.Format)
}

func joinSQL(stmts []string) string {
	if len(stmts) == 0 {
		return "-- nothing to do\n"
	}
	return strings.Join(stmts, "\n"+SplitMarker+"\n") + "\n"
}

func quoteAll(stmts []string) []string {
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = strconv.Quote(s)
	}
	return out
}

var goTmpl = template.Must(template.New("migration").Parse(`package {{.Package}}

import (
	"context"

	"github.com/smatify/tenet/migrations"
)

func init() {
	migrations.Register(migrations.Migration{
		Version:     "{{.Version}}",
		Description: {{.Description}},
{{- if .NoTx}}
		NoTransaction: true,
{{- end}}
		Up: func(ctx context.Context, m *migrations.M) error {
{{- range .Up}}
			m.AddSQL({{.}})
{{- end}}
			return nil
		},
{{- if not .ForwardOnly}}
		Down: func(ctx context.Context, m *migrations.M) error {
{{- range .Down}}
			m.AddSQL({{.}})
{{- end}}
			return nil
		},
{{- end}}
	})
}
`))

// Diff compares the mapped schema of entities with the database and returns
// the statements to migrate up and back down. The migrator's own tables and
// tables for which ignore returns true are left alone.
func (m *Migrator) Diff(ctx context.Context, entities []*mapping.Entity, ignore func(string) bool) (up, down []string, err error) {
	d := m.db.Dialect()
	current, err := schema.Introspect(ctx, m.db, d)
	if err != nil {
		return nil, nil, err
	}
	want := mapping.BuildSchema(entities)
	skip := func(t string) bool { return m.IsOwnTable(t) || (ignore != nil && ignore(t)) }
	ddl := schema.NewDDL(d)
	if up, err = ddl.MigrateSchemas(current, want, skip); err != nil {
		return nil, nil, err
	}
	if down, err = ddl.MigrateSchemas(want, current, skip); err != nil {
		return nil, nil, err
	}
	return up, down, nil
}

var addNotNull = regexp.MustCompile(`(?i)^ALTER TABLE\s+(\S+)\s+ADD\s+(?:COLUMN\s+)?(\S+)\s.*\bNOT NULL\b`)

// NotNullWithoutDefault lists the columns that up adds as NOT NULL without
// a default: on tables with rows the migration fails unless it fills them
// (add a default, update the rows, drop the default).
func NotNullWithoutDefault(up []string) []string {
	var out []string
	for _, s := range up {
		if m := addNotNull.FindStringSubmatch(s); m != nil && !strings.Contains(strings.ToUpper(s), " DEFAULT ") {
			out = append(out, m[1]+"."+m[2])
		}
	}
	return out
}
