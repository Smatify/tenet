package migrations

import (
	"context"

	"github.com/smatify/smorm/migrations"
)

func init() {
	migrations.Register(migrations.Migration{
		Version:     "20261019144202",
		Description: "",
		Up: func(ctx context.Context, m *migrations.M) error {
			m.AddSQL("CREATE TABLE \"author\" (\"id\" INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, \"name\" VARCHAR(100) NOT NULL)")
			m.AddSQL("CREATE UNIQUE INDEX \"uniq_author_name\" ON \"author\" (\"name\")")
			m.AddSQL("CREATE TABLE \"post\" (\"id\" INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, \"title\" VARCHAR(200) NOT NULL, \"author_id\" BIGINT NOT NULL, CONSTRAINT \"fk_post_author_id\" FOREIGN KEY (\"author_id\") REFERENCES \"author\" (\"id\"))")
			m.AddSQL("CREATE INDEX \"idx_post_author_id\" ON \"post\" (\"author_id\")")
			return nil
		},
		Down: func(ctx context.Context, m *migrations.M) error {
			m.AddSQL("DROP TABLE \"post\"")
			m.AddSQL("DROP TABLE \"author\"")
			return nil
		},
	})
}
