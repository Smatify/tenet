# Changelog

## gen v0.1.1 (2026-11-07)

- `go run github.com/smatify/tenet/gen/cmd/tenet@latest` works: the module
  has no replace directive any more.

## v0.1.0 (2026-11-07)

The first release. The API may still change before v1.0.0.

- Entity manager in the style of Doctrine: unit of work, identity map,
  `Persist`/`Remove`/`Flush` in one transaction in dependency order, only
  changed columns written, lifecycle hooks and an event manager, optimistic
  and pessimistic locking, filters and soft delete.
- Mapping with struct tags in two styles: Doctrine (unexported fields,
  generated getters with lazy loading) and Go (exported fields,
  `tenet.Ref[T]`); traits for IDs (int, UUIDv7) and timestamps, embeddables,
  inheritance, generated identity columns, indexes over several fields
  with the order of each column (`index:startsAt desc nulls last|id`).
- Associations (many-to-one, one-to-many, one-to-one, many-to-many) with
  cascades, orphan removal, lazy, extra lazy and eager loading; lazy loads
  batched to avoid N+1; `With` joins to-one associations into the query.
- Queries with typed field references or field names, joins (also on
  conditions, sub queries and lateral), projections read with `Scan`,
  pagination, iteration, DQL and native SQL; `NULLS FIRST/LAST` on every
  database; `In` lists of any length on Postgres; bulk update, insert
  (upserts, streamed from an iterator in batches) and delete.
- Migrations in Go or SQL, generated from the difference between the
  entities and the database, with locking, dry runs and rollback by run.
- Project console (`migrations:*`, `schema:*`, `fixtures:load`) and maker
  (`make:entity`, `make:migration`, `make:fixtures`, …), the `tenet` CLI and
  a vet analyzer.
- Dialects for Postgres, MySQL/MariaDB, SQLite and SQL Server;
  OpenTelemetry tracing in `extra/tenetotel`.
- Go 1.26 or newer.
