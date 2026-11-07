package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
)

// ErrOutOfStock is returned when an order asks for more than is in stock.
var ErrOutOfStock = errors.New("out of stock")

// Item is one line of an order request.
type Item struct {
	SKU      string
	Quantity int
}

// ImportCatalog adds new products and updates name and price of known ones
// (by SKU) in one statement; the stock of known products is kept.
func ImportCatalog(ctx context.Context, db *dbal.DB, products []*Product) (int, error) {
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	return tenet.Insert[Product](em).Rows(products...).
		OnConflict("SKU").
		Set("Name", tenet.Raw("{excluded.Name}")).
		Set("PriceCents", tenet.Raw("{excluded.PriceCents}")).
		Exec(ctx)
}

// PlaceOrder takes the items from stock and stores the order in one
// transaction. When another order changed one of the products in between,
// it starts over with fresh data.
func PlaceOrder(ctx context.Context, db *dbal.DB, customer string, items []Item) (*Order, error) {
	for range 3 {
		em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
		order, err := placeOrder(ctx, em, customer, items)
		if errors.Is(err, tenet.ErrOptimisticLock) {
			continue
		}
		return order, err
	}
	return nil, fmt.Errorf("order of %s: too many concurrent changes", customer)
}

func placeOrder(ctx context.Context, em *tenet.EntityManager, customer string, items []Item) (*Order, error) {
	order := &Order{Customer: customer}
	err := em.Transactional(ctx, func(ctx context.Context, em *tenet.EntityManager) error {
		products := tenet.RepositoryFor[Product](em)
		for _, it := range items {
			p, err := products.FindOneBy(ctx, tenet.Criteria{"SKU": it.SKU})
			if err != nil {
				return fmt.Errorf("%s: %w", it.SKU, err)
			}
			if p.Stock < it.Quantity {
				return fmt.Errorf("%s: %w", it.SKU, ErrOutOfStock)
			}
			p.Stock -= it.Quantity // written with a version check on flush
			order.Lines.Add(&OrderLine{Order: order, Product: p, Quantity: it.Quantity, PriceCents: p.PriceCents})
		}
		return em.Persist(order) // the lines follow (cascade:persist)
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// Sale is one row of the sales report.
type Sale struct {
	SKU     string
	Sold    int
	Revenue int64
}

// SalesReport sums the order lines per product.
func SalesReport(ctx context.Context, db *dbal.DB) ([]Sale, error) {
	em := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	return tenet.Scan[Sale](ctx, tenet.NewQuery[OrderLine](em).As("l").
		Join("l.Product", "p").
		Columns("{p.SKU} AS sku", "sum({l.Quantity}) AS sold", "sum({l.Quantity} * {l.PriceCents}) AS revenue").
		GroupBy("{p.SKU}").
		OrderBy("p.SKU", tenet.Asc))
}
