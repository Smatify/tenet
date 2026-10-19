# smorm

A Doctrine-inspired ORM for Go: unit of work, identity map, transparent lazy
loading, repositories, migrations and a maker — on Postgres, MySQL, MariaDB,
SQLite and SQL Server.

```go
em := smorm.NewEntityManager(db, smorm.WithContext(ctx))

ann := (&entity.Author{}).SetName("Ann")
ann.AddPost((&entity.Post{}).SetTitle("Hello"))
em.Persist(ann)
em.Flush(ctx)                                   // one transaction, correct insert order

post, _ := smorm.Find[entity.Post](ctx, em, id)  // identity map
post.Author().Name()                             // lazy loading (batched, no N+1)

posts, _ := smorm.NewQuery[entity.Post](em).
	Join("Author", "a").
	Where(smorm.Like("a.Name", "A%")).
	OrderBy("Title", smorm.Asc).
	With("Tags").                               // preload
	All(ctx)
```

## Getting started

```bash
go run github.com/smatify/smorm/gen/cmd/smorm init --dialect postgres
go mod tidy
export SMORM_DATABASE_URL=postgres://...
go run ./cmd/console make:entity Post
go run ./cmd/console make:migration
go run ./cmd/console migrations:migrate
```

See [examples/blog](examples/blog) for a complete project generated this way.

## Modules

| Module | Contents |
|---|---|
| `github.com/smatify/smorm` | entity manager, mapping, DBAL + SQL builder, schema tools, migrations, console (no dependencies) |
| `.../dialect/{pg,mysql,sqlite,mssql}dialect` | dialects (drivers are chosen by the application) |
| `.../gen` | code generator, maker (`make:*`), `smorm` CLI |

## Concepts

- **Mapping** with struct tags: `smorm:"length:200,unique"`, `smorm:"manyToOne,inversedBy:posts"`.
  Entities embed `smorm.Entity`; traits `smorm.IntID`, `smorm.UUIDID`, `smorm.Timestamps`.
- **Doctrine style** (default of `make:entity`): unexported fields with generated
  getters that call `smorm.Touch` for lazy loading. **Go style**: exported fields and `smorm.Ref[T]`.
- **Unit of work**: `Persist`, `Remove`, `Flush`, `Refresh`, `Detach`, `Clear`,
  `Transactional`; only changed columns are written; lifecycle hooks and an event manager.
- **Associations**: manyToOne, oneToMany, oneToOne, manyToMany with `smorm.Collection[T]`
  (lazy, extra lazy), cascades, orphan removal, `Preload` and `fetch:eager`.
- **Migrations**: versioned Go or SQL files, `make:migration` diffs entities against the database.

The name `smorm` is defined once in `internal/brand`; `scripts/rebrand.sh <name>`
renames the whole project.
