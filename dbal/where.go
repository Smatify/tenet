package dbal

// whereClause is a list of conditions joined with AND / OR. Groups nest.
type whereClause struct {
	parts []wherePart
}

type wherePart struct {
	sep   string // " AND " or " OR "
	expr  Expr
	group *whereClause
}

func (c *whereClause) add(sep string, e Expr) {
	c.parts = append(c.parts, wherePart{sep: sep, expr: e})
}

func (c *whereClause) addGroup(sep string, fn func(*WhereBuilder)) {
	g := &whereClause{}
	fn(&WhereBuilder{c: g})
	if len(g.parts) > 0 {
		c.parts = append(c.parts, wherePart{sep: sep, group: g})
	}
}

func (c *whereClause) empty() bool { return len(c.parts) == 0 }

func (c *whereClause) appendSQL(w *SQLWriter) error {
	for i, p := range c.parts {
		if i > 0 {
			w.WriteString(p.sep)
		}
		w.WriteString("(")
		var err error
		if p.group != nil {
			err = p.group.appendSQL(w)
		} else {
			err = p.expr.AppendSQL(w)
		}
		if err != nil {
			return err
		}
		w.WriteString(")")
	}
	return nil
}

func (c *whereClause) appendWhere(w *SQLWriter) error {
	if c.empty() {
		return nil
	}
	w.WriteString(" WHERE ")
	return c.appendSQL(w)
}

// WhereBuilder builds a parenthesized group of conditions.
type WhereBuilder struct{ c *whereClause }

// Where adds a condition joined with AND.
func (b *WhereBuilder) Where(query string, args ...any) *WhereBuilder {
	b.c.add(" AND ", SQL(query, args...))
	return b
}

// WhereOr adds a condition joined with OR.
func (b *WhereBuilder) WhereOr(query string, args ...any) *WhereBuilder {
	b.c.add(" OR ", SQL(query, args...))
	return b
}

// WhereExpr adds an expression joined with AND.
func (b *WhereBuilder) WhereExpr(e Expr) *WhereBuilder {
	b.c.add(" AND ", e)
	return b
}

// WhereGroup adds a nested group joined with AND.
func (b *WhereBuilder) WhereGroup(fn func(*WhereBuilder)) *WhereBuilder {
	b.c.addGroup(" AND ", fn)
	return b
}

// WhereOrGroup adds a nested group joined with OR.
func (b *WhereBuilder) WhereOrGroup(fn func(*WhereBuilder)) *WhereBuilder {
	b.c.addGroup(" OR ", fn)
	return b
}
