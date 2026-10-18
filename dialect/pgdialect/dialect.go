package pgdialect

import "github.com/smatify/smorm/dialect"

// Dialect is the PostgreSQL dialect.
type Dialect struct {
	dialect.Base
	features dialect.Feature
}

var _ dialect.Dialect = (*Dialect)(nil)

// New returns a PostgreSQL dialect.
func New() *Dialect {
	return &Dialect{features: dialect.FeatureReturning | dialect.FeatureInsertReturning |
		dialect.FeatureCTE | dialect.FeatureSavepoint | dialect.FeatureReleaseSavepoint |
		dialect.FeatureSequences | dialect.FeatureTransactionalDDL | dialect.FeatureUpsertOnConflict |
		dialect.FeatureMerge | dialect.FeatureLimitOffset | dialect.FeatureSelectForUpdate |
		dialect.FeatureIdentityColumns | dialect.FeatureSkipLocked | dialect.FeatureUpdateFrom}
}

// Name implements dialect.Dialect.
func (d *Dialect) Name() dialect.Name { return dialect.Postgres }

// Features implements dialect.Dialect.
func (d *Dialect) Features() dialect.Feature { return d.features }

// QuoteIdent implements dialect.Dialect.
func (d *Dialect) QuoteIdent(s string) string { return dialect.QuoteWith(s, '"', '"') }

// Placeholder implements dialect.Dialect.
func (d *Dialect) Placeholder(n int) string { return dialect.DollarPlaceholder(n) }
