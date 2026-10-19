# Example: blog

This project was generated end to end by the workflow test
(`gen/internal/e2e`) with nothing but the console:

```bash
go run github.com/smatify/smorm/gen/cmd/smorm init --dialect sqlite
go run ./cmd/console make:entity Post      # title, author (manyToOne -> Author.posts)
go run ./cmd/console make:entity Author    # name (unique)
go run ./cmd/console make:migration
go run ./cmd/console migrations:migrate
go run ./cmd/console make:entity Post      # add subtitle
go run ./cmd/console make:migration
```

Try it:

```bash
export SMORM_DATABASE_URL="file:app.db?_pragma=foreign_keys(1)"
go run ./cmd/console migrations:migrate -n
go run ./cmd/demo
```

`cmd/demo` persists an author with two posts, then loads a post in a new
entity manager and reads `post.Author().Name()` — the author is loaded lazily
by the generated getter.
