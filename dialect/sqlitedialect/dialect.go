package sqlitedialect

import "github.com/smatify/tenet/dialect"

// Dialect is the SQLite (3.35+) dialect.
type Dialect struct {
	dialect.Base
	features dialect.Feature
}

var _ dialect.Dialect = (*Dialect)(nil)

// New returns a SQLite dialect.
func New() *Dialect {
	return &Dialect{features: dialect.FeatureReturning | dialect.FeatureInsertReturning |
		dialect.FeatureCTE | dialect.FeatureSavepoint | dialect.FeatureReleaseSavepoint |
		dialect.FeatureTransactionalDDL | dialect.FeatureUpsertOnConflict | dialect.FeatureLimitOffset |
		dialect.FeatureLastInsertID | dialect.FeatureUpdateFrom | dialect.FeatureNullsOrder}
}

// Name implements dialect.Dialect.
// MaxBindParams implements dialect.BindLimiter: SQLITE_MAX_VARIABLE_NUMBER since 3.32.
func (d *Dialect) MaxBindParams() int { return 32766 }

func (d *Dialect) Name() dialect.Name { return dialect.SQLite }

// Features implements dialect.Dialect.
func (d *Dialect) Features() dialect.Feature { return d.features }

// QuoteIdent implements dialect.Dialect.
func (d *Dialect) QuoteIdent(s string) string { return dialect.QuoteWith(s, '"', '"') }

// Placeholder implements dialect.Dialect.
func (d *Dialect) Placeholder(int) string { return "?" }
