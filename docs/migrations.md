# Migrations

Migrations change the schema step by step, in version order, and record
each step in the database (table `smorm_migrations`). Production databases
change only through migrations; `schema:update` writes the schema straight
from the mapping and is for development.

## Writing migrations

`make:migration` (or `migrations:diff`) compares the entities with the
database and writes the statements that close the gap, up and down.
`migrations:generate` writes an empty one. The project's format (`go` or
`sql`, set by `smorm init --format`, per call `--format`) decides the file:

- **Go**: a file that calls `migrations.Register` with a `Migration`. `Up`
  and `Down` queue statements with `m.AddSQL`; they run after the function
  returns. `m.DB()` serves data migrations (it is nil in a dry run).
- **SQL**: `<version>_<description>.up.sql` and `.down.sql`, loaded with
  `Registry.LoadSQL` from an `embed.FS`. A file runs as one batch unless
  `--smorm:split` lines separate its statements.

Versions are UTC timestamps (`YYYYMMDDHHMMSS`), so their order is the
order they were made in. Read what a generated migration does before you
commit it: a renamed field shows as a dropped and an added column.

## Running them

| Command | Does |
|---|---|
| `migrations:migrate [version\|latest\|first\|prev\|next]` | up or down to the version (default latest) |
| `migrations:status` | which ran and when |
| `migrations:execute <version> --up\|--down` | one migration |
| `migrations:rollback` | the migrations of the last `migrate` run, in reverse |
| `schema:validate` | the mapping is valid and the database matches it |

`--dry-run` prints the SQL instead of running it; `migrations:migrate
--write-sql file` writes it to a file. `-n` takes the default answer of every question
(deployments, CI).

From code, `migrations.New(db, nil)` (nil: the migrations registered with
`migrations.Register`) offers the same: `Migrate`, `DryRun`, `Status`,
`Plan`, `Execute`, `Rollback`.

## Guarantees

- Each migration runs in its own transaction; one that fails is rolled
  back and stops the run, the ones before it stay applied. A migration with
  `NoTransaction` (SQL: a `--smorm:no-transaction` line) runs without one,
  for statements such as `CREATE INDEX CONCURRENTLY`. On MySQL and MariaDB
  DDL commits on its own, so a failed migration can leave part of it behind.
- A lock (table `smorm_migration_locks`) lets one run at a time: of
  several instances that start together one migrates, the others get
  `ErrLocked`. A run that crashed leaves its row behind; delete it by hand.
- `migrations:rollback` reverts the run as a group: the migrations applied
  by the last `migrate` call, not just the last one.
