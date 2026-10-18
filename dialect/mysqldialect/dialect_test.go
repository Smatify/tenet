package mysqldialect

import (
	"testing"

	"github.com/smatify/smorm/dialect"
)

func TestMySQL(t *testing.T) {
	d := New()
	if d.Name() != dialect.MySQL || d.IsMariaDB() {
		t.Fatal("name")
	}
	if got := d.QuoteIdent("a`b"); got != "`a``b`" {
		t.Fatalf("QuoteIdent = %s", got)
	}
	if d.Placeholder(5) != "?" {
		t.Fatal("placeholder")
	}
	if got := d.QuoteString(`it's a \ test`); got != `'it''s a \\ test'` {
		t.Fatalf("QuoteString = %s", got)
	}
	if d.Features().Has(dialect.FeatureInsertReturning) || d.Features().Has(dialect.FeatureReturning) {
		t.Error("MySQL has no RETURNING")
	}
	if !d.Features().Has(dialect.FeatureUpsertOnDuplicate | dialect.FeatureLastInsertID) {
		t.Error("missing features")
	}
}

func TestMariaDB(t *testing.T) {
	d := NewMariaDB()
	if d.Name() != dialect.MariaDB || !d.IsMariaDB() {
		t.Fatal("name")
	}
	if !d.Features().Has(dialect.FeatureInsertReturning | dialect.FeatureSequences) {
		t.Error("MariaDB supports INSERT RETURNING and sequences")
	}
	if d.Features().Has(dialect.FeatureReturning) {
		t.Error("MariaDB has no UPDATE RETURNING")
	}
}
