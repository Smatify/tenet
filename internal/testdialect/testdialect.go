// Package testdialect provides a configurable dialect for unit tests.
package testdialect

import "github.com/smatify/tenet/dialect"

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

// Named is a dialect that reports a real dialect name and mimics its quoting
// and placeholders, for testing name-dependent code in the core module.
type Named struct {
	Dialect
	N dialect.Name
}

// Like returns a Named dialect for n with every feature enabled except the
// ones the real database lacks for statement generation.
func Like(n dialect.Name) *Named {
	d := &Named{Dialect: Dialect{F: ^dialect.Feature(0)}, N: n}
	switch n {
	case dialect.MSSQL:
		d.F &^= dialect.FeatureLimitOffset
	case dialect.MySQL:
		d.F &^= dialect.FeatureReturning | dialect.FeatureInsertReturning | dialect.FeatureOutput
	case dialect.MariaDB:
		d.F &^= dialect.FeatureReturning | dialect.FeatureOutput
	default:
		d.F &^= dialect.FeatureOutput
	}
	return d
}

// Name implements dialect.Dialect.
func (d *Named) Name() dialect.Name { return d.N }

// QuoteIdent implements dialect.Dialect.
func (d *Named) QuoteIdent(s string) string {
	switch d.N {
	case dialect.MySQL, dialect.MariaDB:
		return dialect.QuoteWith(s, '`', '`')
	case dialect.MSSQL:
		return dialect.QuoteWith(s, '[', ']')
	}
	return dialect.QuoteWith(s, '"', '"')
}

// Placeholder implements dialect.Dialect.
func (d *Named) Placeholder(n int) string {
	switch d.N {
	case dialect.Postgres:
		return dialect.DollarPlaceholder(n)
	case dialect.MSSQL:
		return dialect.AtPPlaceholder(n)
	}
	return "?"
}
