package dbtest

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/types"
	"github.com/smatify/smorm/uuid"
)

func init() { Register(Case{"TypeRoundtrip", testTypeRoundtrip}) }

type typeCase struct {
	name  types.Name
	opts  types.Options
	in    any
	out   func() any // returns a pointer to scan into
	equal func(in, out any) bool
}

func deepEqual(in, out any) bool { return reflect.DeepEqual(in, out) }

func testTypeRoundtrip(t *testing.T, db *dbal.DB) {
	moment := time.Date(2026, 10, 18, 12, 34, 56, 123456000, time.FixedZone("CEST", 2*3600))
	id := uuid.NewV7()
	cases := []typeCase{
		{types.String, types.Options{Length: 50}, "héllo ✓ 'quoted'", func() any { return new(string) }, deepEqual},
		{types.String, types.Options{Length: 3, Fixed: true}, "abc", func() any { return new(string) }, deepEqual},
		{types.Text, types.Options{}, string(bytes.Repeat([]byte("long text "), 1000)), func() any { return new(string) }, deepEqual},
		{types.SmallInt, types.Options{}, int16(-1234), func() any { return new(int16) }, deepEqual},
		{types.Integer, types.Options{}, int32(2_000_000_000), func() any { return new(int32) }, deepEqual},
		{types.BigInt, types.Options{}, int64(9_000_000_000_000_000_000), func() any { return new(int64) }, deepEqual},
		{types.Boolean, types.Options{}, true, func() any { return new(bool) }, deepEqual},
		{types.Boolean, types.Options{}, false, func() any { return new(bool) }, deepEqual},
		{types.Decimal, types.Options{Precision: 10, Scale: 2}, "1234.56", func() any { return new(string) }, deepEqual},
		{types.Float, types.Options{}, 3.25, func() any { return new(float64) }, deepEqual},
		{types.Date, types.Options{}, moment, func() any { return new(time.Time) }, func(in, out any) bool {
			a, b := in.(time.Time), out.(time.Time)
			return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
		}},
		{types.Time, types.Options{}, moment, func() any { return new(time.Time) }, func(in, out any) bool {
			a, b := in.(time.Time), out.(time.Time)
			return a.Hour() == b.Hour() && a.Minute() == b.Minute() && a.Second() == b.Second()
		}},
		{types.DateTime, types.Options{}, moment, func() any { return new(time.Time) }, sameInstant},
		{types.DateTimeTZ, types.Options{}, moment, func() any { return new(time.Time) }, sameInstant},
		{types.JSON, types.Options{}, map[string]any{"a": 1.0, "b": []any{"x"}}, func() any { return new(map[string]any) }, deepEqual},
		{types.UUID, types.Options{}, id, func() any { return new(uuid.UUID) }, deepEqual},
		{types.Binary, types.Options{}, []byte{0, 1, 2, 255}, func() any { return new([]byte) }, deepEqual},
		{types.Binary, types.Options{Length: 4}, []byte{9, 8, 7}, func() any { return new([]byte) }, deepEqual},
		{types.Blob, types.Options{}, bytes.Repeat([]byte{7}, 70000), func() any { return new([]byte) }, deepEqual},
	}
	d := db.Dialect()
	for _, tc := range cases {
		t.Run(string(tc.name), func(t *testing.T) {
			typ := types.Default.MustLookup(tc.name)
			roundtrip(t, db, typ.SQLDeclaration(d.Name(), tc.opts), func(v any) (any, error) { return typ.ToDatabase(v, d.Name()) },
				tc.in, func(dest any) any { return typ.ScanTarget(dest, d.Name()) }, tc.out, tc.equal)
		})
	}
	t.Run("NullPointer", func(t *testing.T) {
		typ := types.Default.MustLookup(types.DateTime)
		var in *time.Time
		roundtrip(t, db, typ.SQLDeclaration(d.Name(), types.Options{}), func(v any) (any, error) { return typ.ToDatabase(v, d.Name()) },
			in, func(dest any) any { return typ.ScanTarget(dest, d.Name()) },
			func() any { p := &moment; return &p }, func(_, out any) bool { return out.(*time.Time) == nil })
	})
	t.Run("NullJSON", func(t *testing.T) {
		typ := types.Default.MustLookup(types.JSON)
		var in map[string]any
		roundtrip(t, db, typ.SQLDeclaration(d.Name(), types.Options{}), func(v any) (any, error) { return typ.ToDatabase(v, d.Name()) },
			in, func(dest any) any { return typ.ScanTarget(dest, d.Name()) },
			func() any { return &map[string]any{"x": 1} }, func(_, out any) bool { return out.(map[string]any) == nil })
	})
}

func sameInstant(in, out any) bool { return in.(time.Time).Equal(out.(time.Time)) }

func roundtrip(t *testing.T, db *dbal.DB, decl string, toDB func(any) (any, error), in any,
	target func(dest any) any, out func() any, equal func(in, out any) bool) {
	t.Helper()
	ctx := context.Background()
	d := db.Dialect()
	tbl := d.QuoteIdent(Table(t, db, "id INT PRIMARY KEY, v "+decl+" NULL"))
	v, err := toDB(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, Bind(d, "INSERT INTO "+tbl+" (id, v) VALUES (1, ?)"), v); err != nil {
		t.Fatalf("insert %s: %v", decl, err)
	}
	dest := out()
	if err := db.QueryRowContext(ctx, "SELECT v FROM "+tbl+" WHERE id = 1").Scan(target(dest)); err != nil {
		t.Fatalf("scan %s: %v", decl, err)
	}
	got := reflect.ValueOf(dest).Elem().Interface()
	if !equal(in, got) {
		t.Fatalf("%s roundtrip: in %v (%T), out %v (%T)", decl, in, in, got, got)
	}
}
