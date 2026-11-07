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

Go 1.26 or newer. In production smorm runs on Postgres so far; MySQL/MariaDB,
SQLite and SQL Server pass the same test suite.

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
- **Projections and bulk writes** keep field names where a query is more than "load
  entities": `JoinOn`/`LeftJoinOn` join an entity on a condition, `JoinSub`/`LeftJoinSub` a sub query,
  `JoinLateral`/`LeftJoinLateral` a sub query per row, `Columns` and `GroupBy`
  select expressions (`Column` with arguments, `Distinct`), `smorm.Scan[R]` reads their rows into a
  struct (columns by `db` tag or snake case name) or a single value, `Sub()` uses a query as a sub query (`As`
  names its alias, so a sub query inside can refer to it as plain SQL, `a.name`),
  `smorm.Update[T]` changes, `smorm.Insert[T]` adds and `smorm.Delete[T]` removes many rows in one statement (entities,
  `INSERT … SELECT`, upserts with `OnConflict` and `{excluded.Field}`); listeners get
  `EventPostBulk` with the IDs of the rows after the commit. Bulk writes bypass the unit of
  work: no hooks, loaded entities keep their values, IDs the database generates are not set on
  inserted entities.
  Some of these only run on the databases listed under
  [Database specific features](#database-specific-features).
- **Migrations**: versioned Go or SQL files, `make:migration` diffs entities against the database.

### Database specific features

Most of smorm runs on every dialect. These parts need a database that supports the SQL
they write; on any other, building the statement fails with an error (no SQL is sent):

| Feature | Postgres | SQLite | MySQL / MariaDB | SQL Server |
|---|---|---|---|---|
| `UpdateQuery.From` (`UPDATE … FROM`) | yes | yes | no | no |
| `smorm.Insert[T]` (bulk inserts, upserts) | yes | yes | no | no |
| `JoinLateral`, `LeftJoinLateral` | yes | no | no | no |
| IDs in `EventPostBulk` (`RETURNING`) | yes | yes | nil (any row may have changed) | nil |

## Documentation

- [Lifecycle](docs/lifecycle.md): the entity manager and its identity map,
  identifiers, the order of events within `Flush`, transactions, listeners
  for committed changes, soft delete.
- [Migrations](docs/migrations.md): writing, running and rolling back.
- [Generator and maker](docs/generator.md): `smorm init`, `make:*`,
  generated code, `vet`.
- Examples on [pkg.go.dev](https://pkg.go.dev/github.com/smatify/smorm);
  the ones of the SQLite dialect run with their output.

## Lazy loading

A getter calls `smorm.Touch`; if the entity is an uninitialized reference
(loaded as the target of a to-one association) or a `Collection` that was not
preloaded, it is loaded then, with the context of `smorm.WithContext` /
`em.SetContext`. Touching one reference loads up to `WithBatchSize` (default
100) uninitialized references of the same type in one query, so a list of
posts does not query the author of each post separately.

- `With(...)` on a query or `em.Preload` loads associations up front; prefer
  it for everything a page or job is known to need. `With` joins owning to-one
  associations into the query itself (`LEFT JOIN`, no second statement) unless
  the query locks rows, joins a collection or the target has an active filter.
- A failed lazy load panics with `*smorm.LazyLoadError` (getters return no
  error). `smorm.RecoverLazyLoad` turns it into a 500 response,
  `smorm.CatchLazy` into an error; `smorm.Load(ctx, ptr)` loads explicitly and
  returns the error.
- `smorm.WithoutLazyLoading()` makes every lazy load panic with
  `ErrLazyLoadingDisabled`: use it in tests to find missing `With(...)`.
- `smorm.WithLazyLoadHook(func(l smorm.LazyLoad) {...})` is called before each
  lazy load with the entity and the calling frame, e.g. to log them.

## Benchmarks

`make bench` runs [bench](bench) against Postgres 17 in a container: all libraries on
`database/sql` with pgx, Go 1.27, AMD Ryzen 9 7950X3D, GORM 1.31, bun 1.3, ent 0.14,
sqlc 1.31.

Time per operation (lower is better):

| | smorm | GORM | bun | ent | sqlc |
|---|---:|---:|---:|---:|---:|
| Insert 1 row¹ | 86 µs | 158 µs | 84 µs | 82 µs | 76 µs |
| Insert 100 rows² | 1.66 ms | 1.54 ms | 1.46 ms | 1.57 ms | 7.11 ms |
| Insert 1000 rows² | 15.9 ms | 14.5 ms | 13.5 ms | 15.1 ms | 68.7 ms |
| Find by ID | 60 µs | 60 µs | 158 µs | 57 µs | 54 µs |
| Load 100 rows with their author⁴ | 404 µs | 382 µs | 297 µs | 300 µs | 185 µs |
| Update one field of 100 loaded rows³ | 740 µs | 6.32 ms | 930 µs | 11.9 ms | 5.98 ms |

Allocations per operation:

| | smorm | GORM | bun | ent | sqlc |
|---|---:|---:|---:|---:|---:|
| Insert 1 row¹ | 69 | 86 | 21 | 70 | 18 |
| Insert 100 rows² | 1,737 | 1,103 | 321 | 3,779 | 2,312 |
| Insert 1000 rows² | 17,035 | 11,775 | 3,773 | 38,767 | 23,763 |
| Find by ID | 79 | 73 | 47 | 94 | 25 |
| Load 100 rows with their author⁴ | 2,242 | 2,467 | 644 | 1,802 | 534 |
| Update one field of 100 loaded rows³ | 3,202 | 5,635 | 438 | 11,504 | 1,089 |

¹ GORM writes in a transaction (BEGIN, INSERT, COMMIT); smorm leaves it out when a flush
is one statement without post hooks.
² Multi-row INSERT for all but sqlc, which inserts row by row in a transaction.
³ smorm and bun send one `UPDATE ... FROM (VALUES ...)` (smorm finds the changed
column itself); the others one UPDATE per row in a transaction.
⁴ smorm joins the author into the query (`With` on a to-one association).

The name `smorm` is defined once in `internal/brand`; `scripts/rebrand.sh <name>`
renames the whole project.

## License

[MIT](LICENSE)
