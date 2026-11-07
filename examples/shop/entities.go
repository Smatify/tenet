package main

import "github.com/smatify/tenet"

// Entities in the Go style: exported fields, no generated code.

// Product is an article with its stock. Version guards the stock against
// lost updates by concurrent orders.
type Product struct {
	tenet.Entity `tenet:"table:product"`
	tenet.UUIDID
	tenet.Timestamps
	SKU        string `tenet:"length:32,unique"`
	Name       string `tenet:"length:100"`
	PriceCents int64
	Stock      int
	Version    int `tenet:"version"`
}

// Order is placed by a customer; its lines are stored and removed with it.
type Order struct {
	tenet.Entity `tenet:"table:orders"`
	tenet.UUIDID
	tenet.Timestamps
	Customer string                      `tenet:"length:100"`
	Lines    tenet.Collection[OrderLine] `tenet:"oneToMany,mappedBy:Order,cascade:persist,orphanRemoval"`
}

// OrderLine keeps the price at the time of the order.
type OrderLine struct {
	tenet.Entity `tenet:"table:order_line"`
	tenet.IntID
	Order      *Order   `tenet:"inversedBy:Lines,notnull,onDelete:cascade"`
	Product    *Product `tenet:"notnull"`
	Quantity   int
	PriceCents int64
}

// Total is the sum of the lines.
func (o *Order) Total() int64 {
	var sum int64
	for _, l := range o.Lines.All() {
		sum += int64(l.Quantity) * l.PriceCents
	}
	return sum
}
