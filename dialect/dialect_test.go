package dialect

import (
	"strconv"
	"testing"
)

func TestQuoteWith(t *testing.T) {
	tests := []struct{ in, open, close, want string }{
		{"users", `"`, `"`, `"users"`},
		{`we"ird`, `"`, `"`, `"we""ird"`},
		{"ta`ble", "`", "`", "`ta``ble`"},
		{"a]b", "[", "]", "[a]]b]"},
	}
	for _, tt := range tests {
		if got := QuoteWith(tt.in, tt.open[0], tt.close[0]); got != tt.want {
			t.Errorf("QuoteWith(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBaseQuoteString(t *testing.T) {
	if got := (Base{}).QuoteString("it's"); got != "'it''s'" {
		t.Fatalf("got %s", got)
	}
}

func TestPlaceholders(t *testing.T) {
	if QuestionPlaceholder(3) != "?" || DollarPlaceholder(3) != "$3" || AtPPlaceholder(3) != "@p3" {
		t.Fatal("unexpected placeholder")
	}
}

func TestSavepointSQL(t *testing.T) {
	b := Base{}
	if b.SavepointSQL("sp1") != "SAVEPOINT sp1" ||
		b.ReleaseSavepointSQL("sp1") != "RELEASE SAVEPOINT sp1" ||
		b.RollbackToSavepointSQL("sp1") != "ROLLBACK TO SAVEPOINT sp1" {
		t.Fatal("unexpected savepoint SQL")
	}
}

func TestFeatureHas(t *testing.T) {
	f := FeatureCTE | FeatureReturning
	if !f.Has(FeatureCTE) || !f.Has(FeatureCTE|FeatureReturning) || f.Has(FeatureMerge) {
		t.Fatal("Has is wrong")
	}
}

func TestDollarPlaceholder(t *testing.T) {
	for _, n := range []int{1, 9, 10, 2048, 2049, 70000} {
		if got, want := DollarPlaceholder(n), "$"+strconv.Itoa(n); got != want {
			t.Fatalf("DollarPlaceholder(%d) = %q", n, got)
		}
	}
}
