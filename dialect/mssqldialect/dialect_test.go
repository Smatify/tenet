package mssqldialect

import (
	"testing"

	"github.com/smatify/tenet/dialect"
)

func TestDialect(t *testing.T) {
	d := New()
	if d.Name() != dialect.MSSQL {
		t.Fatal("name")
	}
	if got := d.QuoteIdent("a]b"); got != "[a]]b]" {
		t.Fatalf("QuoteIdent = %s", got)
	}
	if d.Placeholder(4) != "@p4" {
		t.Fatal("placeholder")
	}
	if d.QuoteString("x'y") != "N'x''y'" {
		t.Fatal("QuoteString")
	}
	if d.SavepointSQL("s") != "SAVE TRANSACTION s" || d.ReleaseSavepointSQL("s") != "" ||
		d.RollbackToSavepointSQL("s") != "ROLLBACK TRANSACTION s" {
		t.Error("savepoint SQL")
	}
	if !d.Features().Has(dialect.FeatureOutput|dialect.FeatureOffsetFetch) || d.Features().Has(dialect.FeatureLimitOffset) {
		t.Error("features")
	}
}
