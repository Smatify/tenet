package orm

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/mapping"
)

// DQL is the result of parsing a query in the Doctrine Query Language subset:
//
//	SELECT p FROM Post p JOIN p.author a LEFT JOIN p.tags t
//	WHERE a.name = :name AND (p.views > 10 OR p.title LIKE 'Go%')
//	ORDER BY p.title DESC
//
// Supported: SELECT alias | COUNT(alias) | DISTINCT alias, FROM Entity alias,
// [INNER|LEFT] JOIN alias.association alias, WHERE with =, <>, !=, <, <=,
// >, >=, [NOT] LIKE, [NOT] IN (...) / IN :param, IS [NOT] NULL,
// [NOT] BETWEEN x AND y, AND, OR, NOT and parentheses, ORDER BY ... ASC|DESC.
// Operands are paths (alias.field, alias.embedded.field), :named or ?1
// positional parameters and literals ('text', numbers, TRUE, FALSE, NULL).
// Entity names are Go type names of registered entities.
type DQL struct {
	Query *Query
	Count bool
}

// ParseDQL parses dql into a query on em. params holds named parameters
// (":name") and positional ones ("?1" as key "1").
func (em *EntityManager) ParseDQL(dql string, params map[string]any) (*DQL, error) {
	toks, err := lexDQL(dql)
	if err != nil {
		return nil, err
	}
	p := &dqlParser{em: em, toks: toks, params: params, src: dql, aliases: map[string]string{}}
	return p.parse()
}

type tokKind int

const (
	tIdent tokKind = iota
	tNumber
	tString
	tParam
	tOp
	tPunct
	tEOF
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func lexDQL(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := rune(s[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || s[j] == '_') {
				j++
			}
			out = append(out, token{tIdent, s[i:j], i})
			i = j
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(s) && unicode.IsDigit(rune(s[i+1]))):
			j := i + 1
			for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
				j++
			}
			out = append(out, token{tNumber, s[i:j], i})
			i = j
		case c == '\'':
			var b strings.Builder
			j := i + 1
			for {
				if j >= len(s) {
					return nil, fmt.Errorf("dql: unterminated string at %d", i)
				}
				if s[j] == '\'' {
					if j+1 < len(s) && s[j+1] == '\'' {
						b.WriteByte('\'')
						j += 2
						continue
					}
					break
				}
				b.WriteByte(s[j])
				j++
			}
			out = append(out, token{tString, b.String(), i})
			i = j + 1
		case c == ':' || c == '?':
			j := i + 1
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || s[j] == '_') {
				j++
			}
			if j == i+1 {
				return nil, fmt.Errorf("dql: empty parameter name at %d", i)
			}
			out = append(out, token{tParam, s[i+1 : j], i})
			i = j
		case strings.ContainsRune("<>!=", c):
			j := i + 1
			if j < len(s) && strings.ContainsRune("=>", rune(s[j])) {
				j++
			}
			op := s[i:j]
			switch op {
			case "=", "<>", "!=", "<", "<=", ">", ">=":
			default:
				return nil, fmt.Errorf("dql: unknown operator %q at %d", op, i)
			}
			out = append(out, token{tOp, op, i})
			i = j
		case strings.ContainsRune(".,()", c):
			out = append(out, token{tPunct, string(c), i})
			i++
		default:
			return nil, fmt.Errorf("dql: unexpected character %q at %d", c, i)
		}
	}
	return append(out, token{tEOF, "", len(s)}), nil
}

type dqlParser struct {
	em      *EntityManager
	toks    []token
	i       int
	params  map[string]any
	src     string
	root    string            // DQL alias of the root entity
	aliases map[string]string // DQL alias -> query alias
	q       *Query
}

func (p *dqlParser) peek() token { return p.toks[p.i] }
func (p *dqlParser) next() token { t := p.toks[p.i]; p.i++; return t }

func (p *dqlParser) errf(t token, format string, args ...any) error {
	return fmt.Errorf("dql: %s at position %d", fmt.Sprintf(format, args...), t.pos)
}

func (p *dqlParser) isKeyword(kw ...string) bool {
	t := p.peek()
	if t.kind != tIdent {
		return false
	}
	for _, k := range kw {
		if strings.EqualFold(t.text, k) {
			return true
		}
	}
	return false
}

func (p *dqlParser) expectKeyword(kw string) error {
	if !p.isKeyword(kw) {
		return p.errf(p.peek(), "expected %s, found %q", kw, p.peek().text)
	}
	p.i++
	return nil
}

func (p *dqlParser) expectPunct(s string) error {
	if t := p.peek(); t.kind != tPunct || t.text != s {
		return p.errf(t, "expected %q, found %q", s, t.text)
	}
	p.i++
	return nil
}

func (p *dqlParser) ident() (token, error) {
	t := p.next()
	if t.kind != tIdent {
		return t, p.errf(t, "expected an identifier, found %q", t.text)
	}
	return t, nil
}

func (p *dqlParser) entityByName(name string) (*mapping.Entity, error) {
	for _, e := range p.em.reg.All() {
		if e.Name == name {
			return e, nil
		}
	}
	return nil, fmt.Errorf("dql: unknown entity %q (entities must be registered)", name)
}

func (p *dqlParser) parse() (*DQL, error) {
	res := &DQL{}
	if err := p.expectKeyword("SELECT"); err != nil {
		return nil, err
	}
	distinct := false
	switch {
	case p.isKeyword("COUNT"):
		p.i++
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		res.Count = true
	case p.isKeyword("DISTINCT"):
		p.i++
		distinct = true
	}
	selected, err := p.ident()
	if err != nil {
		return nil, err
	}
	if res.Count {
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
	}
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	entTok, err := p.ident()
	if err != nil {
		return nil, err
	}
	meta, err := p.entityByName(entTok.text)
	if err != nil {
		return nil, err
	}
	aliasTok, err := p.ident()
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(aliasTok.text, selected.text) {
		return nil, p.errf(selected, "only the root alias %q can be selected", aliasTok.text)
	}
	p.root = aliasTok.text
	p.aliases[p.root] = RootAlias
	p.q = p.em.NewQuery(meta.GoType)
	_ = distinct // results are always distinct entities
	for p.isKeyword("JOIN", "INNER", "LEFT") {
		left := p.isKeyword("LEFT")
		if !p.isKeyword("JOIN") {
			p.i++
		}
		if err := p.expectKeyword("JOIN"); err != nil {
			return nil, err
		}
		from, err := p.ident()
		if err != nil {
			return nil, err
		}
		qa, ok := p.aliases[from.text]
		if !ok {
			return nil, p.errf(from, "unknown alias %q", from.text)
		}
		if err := p.expectPunct("."); err != nil {
			return nil, err
		}
		assoc, err := p.ident()
		if err != nil {
			return nil, err
		}
		alias, err := p.ident()
		if err != nil {
			return nil, err
		}
		path := assoc.text
		if qa != RootAlias {
			path = qa + "." + assoc.text
		}
		p.aliases[alias.text] = alias.text
		if left {
			p.q.LeftJoin(path, alias.text)
		} else {
			p.q.Join(path, alias.text)
		}
	}
	if p.isKeyword("WHERE") {
		p.i++
		c, err := p.orExpr()
		if err != nil {
			return nil, err
		}
		p.q.Where(c)
	}
	if p.isKeyword("ORDER") {
		p.i++
		if err := p.expectKeyword("BY"); err != nil {
			return nil, err
		}
		for {
			path, err := p.path()
			if err != nil {
				return nil, err
			}
			dir := dbal.Asc
			if p.isKeyword("ASC") {
				p.i++
			} else if p.isKeyword("DESC") {
				p.i++
				dir = dbal.Desc
			}
			p.q.OrderBy(path, dir)
			if t := p.peek(); t.kind == tPunct && t.text == "," {
				p.i++
				continue
			}
			break
		}
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, p.errf(t, "unexpected %q", t.text)
	}
	if p.q.err != nil {
		return nil, p.q.err
	}
	res.Query = p.q
	return res, nil
}

// path parses alias.field[.sub...] into a query field reference.
func (p *dqlParser) path() (string, error) {
	first, err := p.ident()
	if err != nil {
		return "", err
	}
	qa, ok := p.aliases[first.text]
	if !ok {
		return "", p.errf(first, "unknown alias %q", first.text)
	}
	var parts []string
	for {
		if t := p.peek(); t.kind != tPunct || t.text != "." {
			break
		}
		p.i++
		f, err := p.ident()
		if err != nil {
			return "", err
		}
		parts = append(parts, f.text)
	}
	if len(parts) == 0 {
		return "", p.errf(first, "expected alias.field")
	}
	field := strings.Join(parts, ".")
	if qa == RootAlias {
		return field, nil
	}
	return qa + "." + field, nil
}

func (p *dqlParser) orExpr() (Cond, error) {
	c, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	conds := []Cond{c}
	for p.isKeyword("OR") {
		p.i++
		c, err := p.andExpr()
		if err != nil {
			return nil, err
		}
		conds = append(conds, c)
	}
	if len(conds) == 1 {
		return conds[0], nil
	}
	return Or(conds...), nil
}

func (p *dqlParser) andExpr() (Cond, error) {
	c, err := p.notExpr()
	if err != nil {
		return nil, err
	}
	conds := []Cond{c}
	for p.isKeyword("AND") {
		p.i++
		c, err := p.notExpr()
		if err != nil {
			return nil, err
		}
		conds = append(conds, c)
	}
	if len(conds) == 1 {
		return conds[0], nil
	}
	return And(conds...), nil
}

func (p *dqlParser) notExpr() (Cond, error) {
	if p.isKeyword("NOT") {
		p.i++
		c, err := p.notExpr()
		if err != nil {
			return nil, err
		}
		return Not(c), nil
	}
	if t := p.peek(); t.kind == tPunct && t.text == "(" {
		p.i++
		c, err := p.orExpr()
		if err != nil {
			return nil, err
		}
		return c, p.expectPunct(")")
	}
	return p.comparison()
}

// operand is a parsed right-hand side: a value or a field path.
type operand struct {
	value  any
	path   string
	isPath bool
}

func (p *dqlParser) operand() (operand, error) {
	t := p.peek()
	switch t.kind {
	case tParam:
		p.i++
		v, ok := p.params[t.text]
		if !ok {
			return operand{}, p.errf(t, "missing parameter %q", t.text)
		}
		return operand{value: v}, nil
	case tString:
		p.i++
		return operand{value: t.text}, nil
	case tNumber:
		p.i++
		if n, err := strconv.ParseInt(t.text, 10, 64); err == nil {
			return operand{value: n}, nil
		}
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return operand{}, p.errf(t, "invalid number %q", t.text)
		}
		return operand{value: f}, nil
	case tIdent:
		switch strings.ToUpper(t.text) {
		case "TRUE":
			p.i++
			return operand{value: true}, nil
		case "FALSE":
			p.i++
			return operand{value: false}, nil
		case "NULL":
			p.i++
			return operand{value: nil}, nil
		}
		path, err := p.path()
		return operand{path: path, isPath: true}, err
	}
	return operand{}, p.errf(t, "expected a value, found %q", t.text)
}

func (p *dqlParser) comparison() (Cond, error) {
	field, err := p.path()
	if err != nil {
		return nil, err
	}
	not := false
	if p.isKeyword("NOT") {
		p.i++
		not = true
	}
	wrap := func(c Cond) Cond {
		if not {
			return Not(c)
		}
		return c
	}
	t := p.peek()
	switch {
	case t.kind == tOp && !not:
		p.i++
		rhs, err := p.operand()
		if err != nil {
			return nil, err
		}
		op := t.text
		if op == "!=" {
			op = "<>"
		}
		if rhs.isPath {
			return Raw("{" + field + "} " + op + " {" + rhs.path + "}"), nil
		}
		return cmpCond{field: field, op: op, value: rhs.value}, nil
	case p.isKeyword("LIKE"):
		p.i++
		rhs, err := p.operand()
		if err != nil {
			return nil, err
		}
		if rhs.isPath {
			return nil, p.errf(t, "LIKE needs a value")
		}
		return wrap(cmpCond{field: field, op: "LIKE", value: rhs.value}), nil
	case p.isKeyword("IN"):
		p.i++
		if pt := p.peek(); pt.kind == tParam {
			rhs, err := p.operand()
			if err != nil {
				return nil, err
			}
			if not {
				return NotIn(field, rhs.value), nil
			}
			return In(field, rhs.value), nil
		}
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		var vals []any
		for {
			rhs, err := p.operand()
			if err != nil {
				return nil, err
			}
			if rhs.isPath {
				return nil, p.errf(t, "IN lists need values")
			}
			vals = append(vals, rhs.value)
			if pt := p.peek(); pt.kind == tPunct && pt.text == "," {
				p.i++
				continue
			}
			break
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		if not {
			return NotIn(field, vals), nil
		}
		return In(field, vals), nil
	case p.isKeyword("BETWEEN"):
		p.i++
		lo, err := p.operand()
		if err != nil {
			return nil, err
		}
		if err := p.expectKeyword("AND"); err != nil {
			return nil, err
		}
		hi, err := p.operand()
		if err != nil {
			return nil, err
		}
		if lo.isPath || hi.isPath {
			return nil, p.errf(t, "BETWEEN needs values")
		}
		return wrap(And(Gte(field, lo.value), Lte(field, hi.value))), nil
	case p.isKeyword("IS") && !not:
		p.i++
		isNot := false
		if p.isKeyword("NOT") {
			p.i++
			isNot = true
		}
		if err := p.expectKeyword("NULL"); err != nil {
			return nil, err
		}
		if isNot {
			return IsNotNull(field), nil
		}
		return IsNull(field), nil
	}
	return nil, p.errf(t, "expected a comparison after %s, found %q", field, t.text)
}
