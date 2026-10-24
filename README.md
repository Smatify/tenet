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
	Join(entity.PostFields.Author, "a").
	Where(smorm.Like(entity.AuthorFields.Name, "A%")).
	OrderBy(entity.PostFields.Title, smorm.Asc).
	With(entity.PostFields.Tags).                // preload
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
- **Queries** name fields with the generated refs (`entity.PostFields.Title`) or with
  strings (`"title"`, `"a.name"` for a joined alias, `"author.tags"` as preload path).
  `go run github.com/smatify/smorm/gen/cmd/vet ./...` reports string field names the
  entity does not have, and field accesses without `smorm.Touch`.
  `smorm.Raw("{title} ILIKE ? OR {id} IN ?", pattern, ids)` inserts columns for `{field}`;
  a slice after `IN` is expanded, anywhere else (`= ANY(?)`) it is one array parameter.
- **Migrations**: versioned Go or SQL files, `make:migration` diffs entities against the database.

## Lazy loading

A getter calls `smorm.Touch`; if the entity is an uninitialized reference
(loaded as the target of a to-one association) or a `Collection` that was not
preloaded, it is loaded then, with the context of `smorm.WithContext` /
`em.SetContext`. Touching one reference loads up to `WithBatchSize` (default
100) uninitialized references of the same type in one query, so a list of
posts does not query the author of each post separately.

- `With(...)` on a query or `em.Preload` loads associations up front; prefer
  it for everything a page or job is known to need.
- A failed lazy load panics with `*smorm.LazyLoadError` (getters return no
  error). `smorm.RecoverLazyLoad` turns it into a 500 response,
  `smorm.CatchLazy` into an error; `smorm.Load(ctx, ptr)` loads explicitly and
  returns the error.
- `smorm.WithoutLazyLoading()` makes every lazy load panic with
  `ErrLazyLoadingDisabled`: use it in tests to find missing `With(...)`.
- `smorm.WithLazyLoadHook(func(l smorm.LazyLoad) {...})` is called before each
  lazy load with the entity and the calling frame, e.g. to log them.

The name `smorm` is defined once in `internal/brand`; `scripts/rebrand.sh <name>`
renames the whole project.
