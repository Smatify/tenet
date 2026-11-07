package migrations

import (
	"context"

	"github.com/smatify/tenet/migrations"
)

func init() {
	migrations.Register(migrations.Migration{
		Version:       "20261019144206",
		Description:   "",
		NoTransaction: true,
		Up: func(ctx context.Context, m *migrations.M) error {
			m.AddSQL("ALTER TABLE \"post\" ADD COLUMN \"subtitle\" VARCHAR(255) NULL")
			return nil
		},
		Down: func(ctx context.Context, m *migrations.M) error {
			m.AddSQL("PRAGMA foreign_keys = OFF")
			m.AddSQL("CREATE TABLE \"__tmp__post\" (\"id\" INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, \"title\" VARCHAR(200) NOT NULL, \"author_id\" BIGINT NOT NULL, CONSTRAINT \"fk_post_author_id\" FOREIGN KEY (\"author_id\") REFERENCES \"author\" (\"id\"))")
			m.AddSQL("INSERT INTO \"__tmp__post\" (\"id\", \"title\", \"author_id\") SELECT \"id\", \"title\", \"author_id\" FROM \"post\"")
			m.AddSQL("DROP TABLE \"post\"")
			m.AddSQL("ALTER TABLE \"__tmp__post\" RENAME TO \"post\"")
			m.AddSQL("CREATE INDEX \"idx_post_author_id\" ON \"post\" (\"author_id\")")
			m.AddSQL("PRAGMA foreign_keys = ON")
			return nil
		},
	})
}
