package pgdialect

import (
	"testing"

	"github.com/smatify/tenet/dialect"
)

func TestDialect(t *testing.T) {
	d := New()
	if d.Name() != dialect.Postgres {
		t.Fatal("name")
	}
	if got := d.QuoteIdent(`a"b`); got != `"a""b"` {
		t.Fatalf("QuoteIdent = %s", got)
	}
	if d.Placeholder(2) != "$2" {
		t.Fatal("placeholder")
	}
	if d.QuoteString("it's") != "'it''s'" {
		t.Fatal("QuoteString")
	}
	for _, f := range []dialect.Feature{dialect.FeatureReturning, dialect.FeatureSavepoint, dialect.FeatureUpsertOnConflict, dialect.FeatureTransactionalDDL} {
		if !d.Features().Has(f) {
			t.Errorf("missing feature %b", f)
		}
	}
	if d.Features().Has(dialect.FeatureUpsertOnDuplicate) || d.Features().Has(dialect.FeatureOutput) {
		t.Error("unexpected feature")
	}
	if d.SavepointSQL("s") != "SAVEPOINT s" || d.ReleaseSavepointSQL("s") != "RELEASE SAVEPOINT s" {
		t.Error("savepoint SQL")
	}
}
