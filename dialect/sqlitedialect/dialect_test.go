package sqlitedialect

import (
	"testing"

	"github.com/smatify/tenet/dialect"
)

func TestDialect(t *testing.T) {
	d := New()
	if d.Name() != dialect.SQLite {
		t.Fatal("name")
	}
	if d.QuoteIdent("x") != `"x"` || d.Placeholder(3) != "?" {
		t.Fatal("quote/placeholder")
	}
	if !d.Features().Has(dialect.FeatureReturning | dialect.FeatureUpsertOnConflict | dialect.FeatureSavepoint) {
		t.Error("missing features")
	}
	if d.Features().Has(dialect.FeatureSequences) || d.Features().Has(dialect.FeatureSelectForUpdate) {
		t.Error("unexpected features")
	}
}
