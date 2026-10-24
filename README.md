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
| `.../bench` | benchmarks against GORM, bun, ent and sqlc (not a dependency of anything) |

## Concepts

- **Mapping** with struct tags: `smorm:"length:200,unique"`, `smorm:"manyToOne,inversedBy:posts"`.
  Entities embed `smorm.Entity`; traits `smorm.IntID`, `smorm.UUIDID`, `smorm.Timestamps`.
  `smorm:"generated:identity"` on an integer field other than the identifier lets the
  database number new rows (a public number next to a UUID; unique, read back on
  insert, not on SQLite; a table has at most one identity column).
- **Doctrine style** (default of `make:entity`): unexported fields with generated
  getters that call `smorm.Touch` for lazy loading (`smorm generate --accessors` writes the
  missing ones for fields added by hand). **Go style**: exported fields and `smorm.Ref[T]`.
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

Identifiers, the order of events within `Flush`, transactions, listeners
for committed changes and soft delete: [docs/lifecycle.md](docs/lifecycle.md).

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

## Benchmarks

`make bench` runs [bench](bench) against Postgres 17 in a container. Microseconds
per operation (allocations), all libraries on `database/sql` with pgx, Go 1.27,
AMD Ryzen 9 7950X3D, GORM 1.31, bun 1.3, ent 0.14, sqlc 1.31:

| | smorm | GORM | bun | ent | sqlc |
|---|---:|---:|---:|---:|---:|
| Insert 1 row¹ | 161 (106) | 158 (86) | 84 (21) | 82 (70) | 76 (18) |
| Insert 100 rows² | 1,734 (2,948) | 1,541 (1,103) | 1,460 (321) | 1,572 (3,779) | 7,108 (2,312) |
| Insert 1000 rows² | 15,439 (29,857) | 14,499 (11,775) | 13,543 (3,773) | 15,083 (38,767) | 68,728 (23,763) |
| Find by ID | 59 (101) | 60 (73) | 158 (47) | 57 (94) | 54 (25) |
| Load 100 rows with their author | 451 (3,139) | 382 (2,467) | 297 (644) | 300 (1,802) | 185 (534) |
| Update one field of 100 loaded rows³ | 6,556 (8,000) | 6,319 (5,635) | 930 (438) | 11,924 (11,504) | 5,981 (1,089) |

¹ smorm and GORM write in a transaction (BEGIN, INSERT, COMMIT).
² Multi-row INSERT for all but sqlc, which inserts row by row in a transaction.
³ bun sends one `UPDATE ... FROM (VALUES ...)`; the others one UPDATE per row in a
transaction (smorm finds the changed column itself).

The name `smorm` is defined once in `internal/brand`; `scripts/rebrand.sh <name>`
renames the whole project.
