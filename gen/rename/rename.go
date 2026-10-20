// Package rename rewrites struct tag keys in Go source files, e.g. after
// renaming the project: `old:"length:10"` becomes `new:"length:10"`.
package rename

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Tags renames the struct tag key from to to in every .go file below dir
// (skipping vendor, testdata and hidden directories). It returns the changed
// files. Only tag literals are touched; the rest of the file is unchanged.
func Tags(dir, from, to string) ([]string, error) {
	if !validKey(from) || !validKey(to) {
		return nil, fmt.Errorf("rename: tag keys must be non-empty identifiers without spaces, quotes or colons")
	}
	var changed []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		ok, err := File(path, from, to)
		if err != nil {
			return err
		}
		if ok {
			changed = append(changed, path)
		}
		return nil
	})
	return changed, err
}

func validKey(k string) bool {
	return k != "" && !strings.ContainsAny(k, " \t\":`,")
}

// File renames the tag key in one file and reports whether it changed.
func File(path, from, to string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, changed, err := Source(path, src, from, to)
	if err != nil || !changed {
		return false, err
	}
	return true, os.WriteFile(path, out, 0o644)
}

// Source renames the tag key in src.
func Source(name string, src []byte, from, to string) ([]byte, bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false, err
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	ast.Inspect(f, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok || field.Tag == nil {
			return true
		}
		value, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return true
		}
		renamed, ok := renameKey(value, from, to)
		if !ok {
			return true
		}
		lit := "`" + renamed + "`"
		if strings.Contains(renamed, "`") {
			lit = strconv.Quote(renamed)
		}
		edits = append(edits, edit{fset.Position(field.Tag.Pos()).Offset, fset.Position(field.Tag.End()).Offset, lit})
		return true
	})
	if len(edits) == 0 {
		return src, false, nil
	}
	var buf bytes.Buffer
	last := 0
	for _, e := range edits {
		buf.Write(src[last:e.start])
		buf.WriteString(e.text)
		last = e.end
	}
	buf.Write(src[last:])
	return buf.Bytes(), true, nil
}

// renameKey rewrites the key of a conventional struct tag
// (key:"value" pairs separated by spaces).
func renameKey(tag, from, to string) (string, bool) {
	var b strings.Builder
	changed := false
	rest := tag
	for rest != "" {
		// leading spaces
		i := 0
		for i < len(rest) && rest[i] == ' ' {
			i++
		}
		b.WriteString(rest[:i])
		rest = rest[i:]
		if rest == "" {
			break
		}
		// key
		i = 0
		for i < len(rest) && rest[i] > ' ' && rest[i] != ':' && rest[i] != '"' && rest[i] != 0x7f {
			i++
		}
		if i == 0 || i+1 >= len(rest) || rest[i] != ':' || rest[i+1] != '"' {
			b.WriteString(rest) // not a conventional tag: keep as is
			break
		}
		key := rest[:i]
		rest = rest[i+1:]
		// quoted value
		j := 1
		for j < len(rest) && rest[j] != '"' {
			if rest[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(rest) {
			b.WriteString(key + ":" + rest)
			break
		}
		if key == from {
			key = to
			changed = true
		}
		b.WriteString(key + ":" + rest[:j+1])
		rest = rest[j+1:]
	}
	return b.String(), changed
}
