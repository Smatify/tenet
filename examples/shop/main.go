// Command shop imports a product catalog, places orders against the stock
// and prints a sales report, in an SQLite database in memory.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite"

	"github.com/smatify/tenet"
	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/dialect/sqlitedialect"
	"github.com/smatify/tenet/mapping"
	"github.com/smatify/tenet/schema"
)

func main() {
	ctx := context.Background()
	db, err := open(ctx)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if err := run(ctx, db, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// open creates the database and its tables. An application keeps its
// schema in migrations (see examples/blog); this one creates it directly.
func open(ctx context.Context) (*dbal.DB, error) {
	db, err := dbal.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)", sqlitedialect.New())
	if err != nil {
		return nil, err
	}
	db.SQL().SetMaxOpenConns(1) // one connection: the database lives in it
	reg := mapping.NewRegistry()
	if err := reg.Register(&Product{}, &Order{}, &OrderLine{}); err != nil {
		return nil, err
	}
	stmts, err := schema.NewDDL(db.Dialect()).CreateSchema(mapping.BuildSchema(reg.All()))
	if err != nil {
		return nil, err
	}
	for _, s := range stmts {
		if _, err := db.SQL().ExecContext(ctx, s); err != nil {
			return nil, err
		}
	}
	return db, nil
}

func run(ctx context.Context, db *dbal.DB, out io.Writer) error {
	n, err := ImportCatalog(ctx, db, []*Product{
		{SKU: "MUG", Name: "Mug", PriceCents: 900, Stock: 10},
		{SKU: "TEE", Name: "T-shirt", PriceCents: 1900, Stock: 3},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "imported %d products\n", n)
	// The second import changes the price; the stock stays.
	if _, err := ImportCatalog(ctx, db, []*Product{{SKU: "MUG", Name: "Mug", PriceCents: 1000}}); err != nil {
		return err
	}

	order, err := PlaceOrder(ctx, db, "ann@example.com", []Item{{"MUG", 2}, {"TEE", 1}})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "order of %s: %d lines, %.2f EUR\n", order.Customer, order.Lines.Len(), float64(order.Total())/100)

	_, err = PlaceOrder(ctx, db, "bob@example.com", []Item{{"MUG", 1}, {"TEE", 5}})
	fmt.Fprintln(out, "order of bob@example.com:", err)

	// Two entity managers change the same product: the second flush fails
	// instead of overwriting the first.
	em1 := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	em2 := tenet.NewEntityManager(db, tenet.WithContext(ctx))
	mug1, err := tenet.RepositoryFor[Product](em1).FindOneBy(ctx, tenet.Criteria{"SKU": "MUG"})
	if err != nil {
		return err
	}
	mug2, err := tenet.RepositoryFor[Product](em2).FindOneBy(ctx, tenet.Criteria{"SKU": "MUG"})
	if err != nil {
		return err
	}
	mug1.Stock, mug2.Stock = 20, 30
	if err := em1.Flush(ctx); err != nil {
		return err
	}
	err = em2.Flush(ctx)
	fmt.Fprintln(out, "second change of MUG:", errors.Is(err, tenet.ErrOptimisticLock))

	sales, err := SalesReport(ctx, db)
	if err != nil {
		return err
	}
	for _, s := range sales {
		fmt.Fprintf(out, "%-4s sold %d for %.2f EUR\n", s.SKU, s.Sold, float64(s.Revenue)/100)
	}
	return nil
}
