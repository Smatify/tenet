// Package dialect defines the contract every database dialect implements.
// Concrete dialects live in their own modules (pgdialect, mysqldialect, ...)
// so applications only pull in the drivers they need.
package dialect

import (
	"strconv"
	"sync"
)

// Name identifies a dialect.
type Name string

// Known dialect names.
const (
	Invalid  Name = ""
	Postgres Name = "postgres"
	MySQL    Name = "mysql"
	MariaDB  Name = "mariadb"
	SQLite   Name = "sqlite"
	MSSQL    Name = "mssql"
)

// Feature is a bit set of optional capabilities.
type Feature uint64

// Optional capabilities a dialect may support.
const (
	FeatureReturning       Feature = 1 << iota // INSERT/UPDATE/DELETE ... RETURNING
	FeatureInsertReturning                     // INSERT ... RETURNING (subset, e.g. MariaDB)
	FeatureOutput                              // MSSQL OUTPUT INSERTED.*
	FeatureCTE
	FeatureSavepoint
	FeatureReleaseSavepoint
	FeatureSequences
	FeatureTransactionalDDL
	FeatureUpsertOnConflict  // ON CONFLICT (...) DO UPDATE
	FeatureUpsertOnDuplicate // ON DUPLICATE KEY UPDATE
	FeatureMerge             // MERGE statement
	FeatureLimitOffset       // LIMIT n OFFSET m
	FeatureOffsetFetch       // OFFSET m ROWS FETCH NEXT n ROWS ONLY
	FeatureSelectForUpdate
	FeatureIdentityColumns
	FeatureLastInsertID
	FeatureSkipLocked
	FeatureDeleteJoin
	FeatureUpdateFrom
	FeatureArrayParams // a slice as one array parameter: x = ANY(?)
	FeatureNullsOrder  // ORDER BY x NULLS FIRST / NULLS LAST
)

// Has reports whether all of the given features are set.
func (f Feature) Has(other Feature) bool { return f&other == other }

// Dialect is the database specific part of tenet.
type Dialect interface {
	// Name returns the dialect name.
	Name() Name
	// Features returns the supported optional features.
	Features() Feature
	// QuoteIdent quotes a single identifier (no dots handling).
	QuoteIdent(ident string) string
	// Placeholder returns the bind parameter placeholder for the n-th (1-based) argument.
	Placeholder(n int) string
	// QuoteString returns s as a SQL string literal (used for debugging and DDL defaults).
	QuoteString(s string) string

	// SavepointSQL returns the statement that creates a savepoint.
	SavepointSQL(name string) string
	// ReleaseSavepointSQL returns the statement that releases a savepoint or "" if unsupported.
	ReleaseSavepointSQL(name string) string
	// RollbackToSavepointSQL returns the statement that rolls back to a savepoint.
	RollbackToSavepointSQL(name string) string
}

// BindLimiter is implemented by dialects whose databases take more bind
// parameters per statement than DefaultMaxBindParams: batched inserts and
// updates then send fewer, larger statements.
type BindLimiter interface {
	MaxBindParams() int
}

// DefaultMaxBindParams is the bind parameter limit of a dialect without
// BindLimiter (SQL Server allows 2100 per statement).
const DefaultMaxBindParams = 2000

// MaxBindParams returns the bind parameter limit of d.
func MaxBindParams(d Dialect) int {
	if l, ok := d.(BindLimiter); ok && l.MaxBindParams() > 0 {
		return l.MaxBindParams()
	}
	return DefaultMaxBindParams
}

// Base implements the parts of Dialect that are identical for most databases.
// Dialects embed it and override what differs.
type Base struct{}

// QuoteString quotes s using standard SQL single quotes.
func (Base) QuoteString(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			b = append(b, '\'')
		}
		b = append(b, s[i])
	}
	return string(append(b, '\''))
}

// SavepointSQL implements Dialect.
func (Base) SavepointSQL(name string) string { return "SAVEPOINT " + name }

// ReleaseSavepointSQL implements Dialect.
func (Base) ReleaseSavepointSQL(name string) string { return "RELEASE SAVEPOINT " + name }

// RollbackToSavepointSQL implements Dialect.
func (Base) RollbackToSavepointSQL(name string) string { return "ROLLBACK TO SAVEPOINT " + name }

// QuoteWith wraps ident in open/close and doubles any embedded close character.
func QuoteWith(ident string, open, close byte) string {
	b := make([]byte, 0, len(ident)+2)
	b = append(b, open)
	for i := 0; i < len(ident); i++ {
		if ident[i] == close {
			b = append(b, close)
		}
		b = append(b, ident[i])
	}
	return string(append(b, close))
}

// QuestionPlaceholder returns "?" regardless of position.
func QuestionPlaceholder(int) string { return "?" }

// DollarPlaceholder returns "$n".
func DollarPlaceholder(n int) string {
	if t := dollarPlaceholders(); n > 0 && n <= len(t) {
		return t[n-1]
	}
	return "$" + strconv.Itoa(n)
}

// dollarPlaceholders holds "$1" to "$2048", so that writing a statement
// with many parameters does not allocate one string per parameter.
var dollarPlaceholders = sync.OnceValue(func() []string {
	t := make([]string, 2048)
	for i := range t {
		t[i] = "$" + strconv.Itoa(i+1)
	}
	return t
})

// AtPPlaceholder returns "@pn" (MSSQL).
func AtPPlaceholder(n int) string { return "@p" + strconv.Itoa(n) }
