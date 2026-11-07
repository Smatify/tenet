# Example: shop

Orders against a stock of products, with entities in the Go style (exported
fields, no generated code) in an SQLite database in memory:

```bash
go run .
```

- [entities.go](entities.go): UUID identifiers and timestamps from traits, a
  `version` field for optimistic locking, order lines stored and removed with
  their order (`cascade:persist,orphanRemoval`).
- [shop.go](shop.go):
  - `ImportCatalog` upserts products by SKU in one statement (`tenet.Insert`
    with `OnConflict`).
  - `PlaceOrder` checks and lowers the stock and stores the order in one
    transaction (`em.Transactional`), and starts over when a concurrent order
    changed a product (`tenet.ErrOptimisticLock`).
  - `SalesReport` sums the order lines per product into a struct
    (`tenet.Scan`).
