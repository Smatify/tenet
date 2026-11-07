# Lifecycle of entities and flushes

## Entity manager

- An `EntityManager` is a unit of work: an identity map of the loaded
  entities and the changes to write. It is **not safe for concurrent use**;
  create one per HTTP request, job or goroutine.
- It stores a context (`tenet.WithContext(ctx)`, `em.SetContext(ctx)`) for
  lazy loading, because getters take no context. Methods with a `ctx`
  argument use that one for the call itself.
- A failed `Flush` closes it (`IsOpen()` is false, everything returns
  `ErrClosed`): the database rolled back, but the entities still hold the
  changes. Start over with a new entity manager.

## Identity map

- Each row is loaded once per entity manager: `Find`, queries and lazy
  loads of the same row return the same pointer, so a change made through
  one is seen through all of them, and `Flush` writes it once.
- A row already in the map is not overwritten by a later query: the entity
  keeps the values (and unflushed changes) it has. `Refresh` reloads one
  entity from the database and discards its unflushed changes.
- An entity manager therefore grows stale and large if it lives long: keep
  one per HTTP request, job or event, not one per process. A long job over
  many rows flushes and calls `Clear` in batches (or uses `Detach` for the
  entities it is done with) to keep memory flat; entities from before the
  `Clear` are no longer managed and are not written. A job that adds many
  rows writes them with `tenet.Insert[T](em).RowsFrom(seq)` instead: no
  unit of work, and only the rows of one statement in memory at a time.
- Entities belong to the entity manager that loaded or persisted them. An
  entity of another entity manager reached through a relationship is linked
  when it is stored already, and reported (`NewEntityFoundError`) when it
  is new and the relationship does not cascade persist.

## Identifiers

- `Persist` assigns UUID identifiers (`tenet.UUIDID`, `generated:uuid7`)
  immediately, so a new entity can be referenced by ID before the flush.
- Integer identities (`tenet.IntID`), sequences and `generated:identity`
  fields get their value from the database during `Flush`.

## Order within Flush

1. `prePersist` for every new entity (also those reached through
   `cascade:persist`).
2. Change sets are computed: `preUpdate` for changed entities (changes made
   there are written too), `preRemove` for removed ones.
3. `onFlush` once, if there is anything to write.
4. The writes in one transaction, in dependency order: inserts, updates,
   join tables, deletes. `postPersist`, `postUpdate` and `postRemove` run
   right after the write of each entity, **inside the transaction**: an
   error returned there rolls everything back.
   A flush that writes a single statement of a single-table entity and has
   no such hook or listener runs without a transaction (one round trip
   instead of three); the statement is atomic on its own.
5. After the commit: snapshots are taken (the entities count as unchanged)
   and `postFlush` runs once.

If the writes fail, the transaction is rolled back, the entity manager is
closed and `onRollback` runs instead of `postFlush`.

`em.Transactional(ctx, fn)` runs `fn` and a final flush in one transaction.
`postFlush` of every flush inside runs after that transaction commits;
if it rolls back, `onRollback` runs. An entity manager created on a
transaction you opened yourself (`dbal.Tx`) cannot know when you commit:
its `postFlush` runs after the writes, before your commit.

## Events

Entities may implement hooks (`PrePersist(ctx) error`, `PreUpdate(ctx, *ChangeSet) error`,
`PostLoad(ctx) error`, ...). Listeners for all entities go to an
`EventManager`, which many entity managers share:

```go
events := tenet.NewEventManager()
em := tenet.NewEntityManager(db, tenet.WithContext(ctx), tenet.WithEvents(events))
```

Listeners get the entity manager in `args.EM`. Work that must only happen
for committed data outside the database (search index, cache
invalidation, messages) collects in the post events and acts in `postFlush`,
keyed by the entity manager, and forgets on `onRollback`:

```go
var mu sync.Mutex
changed := map[*tenet.EntityManager][]any{}

events.On(func(ctx context.Context, a *tenet.EventArgs) error {
	mu.Lock()
	defer mu.Unlock()
	changed[a.EM] = append(changed[a.EM], a.Entity)
	return nil
}, tenet.EventPostPersist, tenet.EventPostUpdate, tenet.EventPostRemove)

events.On(func(ctx context.Context, a *tenet.EventArgs) error {
	mu.Lock()
	items := changed[a.EM]
	delete(changed, a.EM)
	mu.Unlock()
	return index.Update(ctx, items) // committed: safe to publish
}, tenet.EventPostFlush)

events.On(func(ctx context.Context, a *tenet.EventArgs) error {
	mu.Lock()
	delete(changed, a.EM)
	mu.Unlock()
	return nil
}, tenet.EventOnRollback)
```

An error returned from `postFlush` is returned by `Flush`, but the data is
committed already.

## Soft delete

Entities with `tenet.SoftDelete` get a `deleted_at` column and the
`tenet.SoftDeleteFilterName` filter:

- `Remove` sets `deleted_at` (an UPDATE, followed by `postRemove`); the
  entity leaves the entity manager.
- Queries and collections hide deleted rows. `Query.WithDeleted()` shows
  them for one query, `em.Filters().Disable(tenet.SoftDeleteFilterName)` for the
  entity manager.
- References to a deleted row still load (an audit entry keeps showing the
  deleted record it refers to).
- `Remove` of an entity that is already deleted (loaded with
  `WithDeleted`) deletes the row.
