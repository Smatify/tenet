package brand

import (
	"regexp"
	"strings"
	"testing"
)

func TestDerivedNames(t *testing.T) {
	derived := map[string]string{
		"TagKey":              TagKey,
		"CLIName":             CLIName,
		"MigrationsTable":     MigrationsTable,
		"MigrationLocksTable": MigrationLocksTable,
		"GeneratedSuffix":     GeneratedSuffix,
		"ConfigFile":          ConfigFile,
		"GeneratedHeader":     GeneratedHeader,
	}
	for k, v := range derived {
		if !strings.Contains(v, Name) {
			t.Errorf("%s = %q does not derive from Name", k, v)
		}
	}
}

func TestNameIsValidIdentifier(t *testing.T) {
	if !regexp.MustCompile(`^[a-z][a-z0-9]*$`).MatchString(Name) {
		t.Fatalf("Name %q must be a lowercase identifier usable as tag key and package name", Name)
	}
}

func TestEnv(t *testing.T) {
	got := Env("database_url")
	want := strings.ToUpper(Name) + "_DATABASE_URL"
	if got != want {
		t.Fatalf("Env() = %q, want %q", got, want)
	}
}

func TestGeneratedHeaderMatchesGoConvention(t *testing.T) {
	re := regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)
	if !re.MatchString(GeneratedHeader) {
		t.Fatalf("GeneratedHeader %q does not match the Go convention", GeneratedHeader)
	}
}
