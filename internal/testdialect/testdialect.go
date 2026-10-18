// Package testdialect provides a configurable dialect for unit tests.
package testdialect

import "github.com/smatify/smorm/dialect"

// Dialect is a generic ANSI-like dialect with configurable features.
type Dialect struct {
	dialect.Base
	F      dialect.Feature
	Dollar bool // use $n placeholders instead of ?
}

// New returns a dialect that supports every feature and uses ? placeholders.
func New() *Dialect { return &Dialect{F: ^dialect.Feature(0)} }

// Name implements dialect.Dialect.
func (d *Dialect) Name() dialect.Name { return "test" }

// Features implements dialect.Dialect.
func (d *Dialect) Features() dialect.Feature { return d.F }

// QuoteIdent implements dialect.Dialect.
func (d *Dialect) QuoteIdent(s string) string { return dialect.QuoteWith(s, '"', '"') }

// Placeholder implements dialect.Dialect.
func (d *Dialect) Placeholder(n int) string {
	if d.Dollar {
		return dialect.DollarPlaceholder(n)
	}
	return "?"
}
