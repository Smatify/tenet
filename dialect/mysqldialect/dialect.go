package mysqldialect

import (
	"strings"

	"github.com/smatify/smorm/dialect"
)

// Dialect is the MySQL / MariaDB dialect.
type Dialect struct {
	dialect.Base
	name     dialect.Name
	features dialect.Feature
}

var _ dialect.Dialect = (*Dialect)(nil)

const common = dialect.FeatureCTE | dialect.FeatureSavepoint | dialect.FeatureReleaseSavepoint |
	dialect.FeatureUpsertOnDuplicate | dialect.FeatureLimitOffset | dialect.FeatureSelectForUpdate |
	dialect.FeatureLastInsertID | dialect.FeatureSkipLocked | dialect.FeatureDeleteJoin

// New returns a MySQL (8.0+) dialect.
func New() *Dialect { return &Dialect{name: dialect.MySQL, features: common} }

// NewMariaDB returns a MariaDB (10.5+) dialect, which additionally supports
// INSERT/DELETE ... RETURNING and sequences.
func NewMariaDB() *Dialect {
	return &Dialect{name: dialect.MariaDB, features: common | dialect.FeatureInsertReturning | dialect.FeatureSequences}
}

// Name implements dialect.Dialect.
// MaxBindParams implements dialect.BindLimiter: the protocol counts parameters in 16 bits.
func (d *Dialect) MaxBindParams() int { return 65535 }

func (d *Dialect) Name() dialect.Name { return d.name }

// IsMariaDB reports whether the dialect targets MariaDB.
func (d *Dialect) IsMariaDB() bool { return d.name == dialect.MariaDB }

// Features implements dialect.Dialect.
func (d *Dialect) Features() dialect.Feature { return d.features }

// QuoteIdent implements dialect.Dialect.
func (d *Dialect) QuoteIdent(s string) string { return dialect.QuoteWith(s, '`', '`') }

// Placeholder implements dialect.Dialect.
func (d *Dialect) Placeholder(int) string { return "?" }

// QuoteString escapes backslashes as well, because MySQL treats them as escape
// characters unless NO_BACKSLASH_ESCAPES is set.
func (d *Dialect) QuoteString(s string) string {
	return d.Base.QuoteString(strings.ReplaceAll(s, `\`, `\\`))
}
