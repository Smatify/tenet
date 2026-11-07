# Example: blog

Entities, migrations and console of this project were generated with nothing
but the console (the workflow test `gen/internal/e2e` repeats these steps):

```bash
go run github.com/smatify/tenet/gen/cmd/tenet@latest init --dialect sqlite
go run ./cmd/console make:entity Post      # title, author (manyToOne -> Author.posts)
go run ./cmd/console make:entity Author    # name (unique)
go run ./cmd/console make:migration
go run ./cmd/console migrations:migrate
go run ./cmd/console make:entity Post      # add subtitle
go run ./cmd/console make:migration
```

Try it:

```bash
export TENET_DATABASE_URL="file:app.db?_pragma=foreign_keys(1)"
go run ./cmd/console migrations:migrate -n
go run ./cmd/demo
```

[cmd/demo](cmd/demo/main.go) persists two authors with their posts, then
queries with the generated field references (`entity.PostFields.Author`),
loads an author lazily through the generated getter, counts posts per author
with `tenet.Scan` and updates many rows in one statement. `go test ./...`
runs it against SQLite in memory.
