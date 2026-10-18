package mssqldialect

import "github.com/smatify/smorm/dialect"

// Dialect is the Microsoft SQL Server (2019+) dialect.
type Dialect struct {
	dialect.Base
	features dialect.Feature
}

var _ dialect.Dialect = (*Dialect)(nil)

// New returns a SQL Server dialect.
func New() *Dialect {
	return &Dialect{features: dialect.FeatureOutput | dialect.FeatureCTE | dialect.FeatureSavepoint |
		dialect.FeatureSequences | dialect.FeatureTransactionalDDL | dialect.FeatureMerge |
		dialect.FeatureOffsetFetch | dialect.FeatureIdentityColumns | dialect.FeatureUpdateFrom |
		dialect.FeatureDeleteJoin}
}

// Name implements dialect.Dialect.
func (d *Dialect) Name() dialect.Name { return dialect.MSSQL }

// Features implements dialect.Dialect.
func (d *Dialect) Features() dialect.Feature { return d.features }

// QuoteIdent implements dialect.Dialect.
func (d *Dialect) QuoteIdent(s string) string { return dialect.QuoteWith(s, '[', ']') }

// Placeholder implements dialect.Dialect.
func (d *Dialect) Placeholder(n int) string { return dialect.AtPPlaceholder(n) }

// QuoteString returns an N” unicode literal.
func (d *Dialect) QuoteString(s string) string { return "N" + d.Base.QuoteString(s) }

// SavepointSQL implements dialect.Dialect.
func (d *Dialect) SavepointSQL(name string) string { return "SAVE TRANSACTION " + name }

// ReleaseSavepointSQL implements dialect.Dialect; SQL Server cannot release savepoints.
func (d *Dialect) ReleaseSavepointSQL(string) string { return "" }

// RollbackToSavepointSQL implements dialect.Dialect.
func (d *Dialect) RollbackToSavepointSQL(name string) string { return "ROLLBACK TRANSACTION " + name }
