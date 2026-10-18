package mapping

import (
	"strings"
	"unicode"
)

// NamingStrategy derives database names from Go names when the mapping does
// not specify them explicitly.
type NamingStrategy interface {
	// TableName returns the table for an entity type name, e.g. BlogPost -> blog_post.
	TableName(typeName string) string
	// ColumnName returns the column for a field name, e.g. CreatedAt -> created_at.
	ColumnName(fieldName string) string
	// JoinColumnName returns the foreign key column for an association, e.g. Author -> author_id.
	JoinColumnName(assocName, referencedColumn string) string
	// JoinTableName returns the join table for a many-to-many association.
	JoinTableName(ownerTable, targetTable string) string
}

// SnakeCase is the default naming strategy (like Doctrine's underscore
// strategy): names are snake_cased, tables are not pluralized.
type SnakeCase struct{}

// TableName implements NamingStrategy.
func (SnakeCase) TableName(typeName string) string { return ToSnake(typeName) }

// ColumnName implements NamingStrategy.
func (SnakeCase) ColumnName(fieldName string) string { return ToSnake(fieldName) }

// JoinColumnName implements NamingStrategy.
func (SnakeCase) JoinColumnName(assocName, referencedColumn string) string {
	return ToSnake(assocName) + "_" + referencedColumn
}

// JoinTableName implements NamingStrategy.
func (SnakeCase) JoinTableName(ownerTable, targetTable string) string {
	return ownerTable + "_" + targetTable
}

// ToSnake converts CamelCase to snake_case, keeping acronyms together:
// "UserID" -> "user_id", "HTTPServer" -> "http_server", "address2" -> "address2".
func ToSnake(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
