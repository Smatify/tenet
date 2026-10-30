package types

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/uuid"
)

var dialects = []dialect.Name{dialect.Postgres, dialect.MySQL, dialect.MariaDB, dialect.SQLite, dialect.MSSQL}

func TestSQLDeclaration(t *testing.T) {
	r := NewRegistry()
	tests := []struct {
		name Name
		opts Options
		want map[dialect.Name]string
	}{
		{String, Options{}, map[dialect.Name]string{dialect.Postgres: "VARCHAR(255)", dialect.MySQL: "VARCHAR(255)", dialect.SQLite: "VARCHAR(255)", dialect.MSSQL: "NVARCHAR(255)"}},
		{String, Options{Length: 2, Fixed: true}, map[dialect.Name]string{dialect.Postgres: "CHAR(2)", dialect.MSSQL: "NCHAR(2)"}},
		{Text, Options{}, map[dialect.Name]string{dialect.Postgres: "TEXT", dialect.MariaDB: "LONGTEXT", dialect.MSSQL: "NVARCHAR(MAX)"}},
		{SmallInt, Options{}, map[dialect.Name]string{dialect.Postgres: "SMALLINT", dialect.MSSQL: "SMALLINT"}},
		{Integer, Options{}, map[dialect.Name]string{dialect.Postgres: "INTEGER", dialect.MySQL: "INT", dialect.MSSQL: "INT", dialect.SQLite: "INTEGER"}},
		{BigInt, Options{}, map[dialect.Name]string{dialect.Postgres: "BIGINT", dialect.SQLite: "BIGINT"}},
		{Boolean, Options{}, map[dialect.Name]string{dialect.Postgres: "BOOLEAN", dialect.MySQL: "TINYINT(1)", dialect.MSSQL: "BIT", dialect.SQLite: "BOOLEAN"}},
		{Decimal, Options{Precision: 12, Scale: 2}, map[dialect.Name]string{dialect.Postgres: "NUMERIC(12, 2)", dialect.MySQL: "DECIMAL(12, 2)"}},
		{Decimal, Options{}, map[dialect.Name]string{dialect.MSSQL: "DECIMAL(10, 0)"}},
		{Float, Options{}, map[dialect.Name]string{dialect.Postgres: "DOUBLE PRECISION", dialect.MySQL: "DOUBLE", dialect.SQLite: "REAL", dialect.MSSQL: "FLOAT"}},
		{Date, Options{}, map[dialect.Name]string{dialect.Postgres: "DATE", dialect.MSSQL: "DATE"}},
		{Time, Options{}, map[dialect.Name]string{dialect.Postgres: "TIME(6)", dialect.MySQL: "TIME(6)", dialect.MSSQL: "TIME", dialect.SQLite: "TIME"}},
		{DateTime, Options{}, map[dialect.Name]string{dialect.Postgres: "TIMESTAMP(6) WITHOUT TIME ZONE", dialect.MySQL: "DATETIME(6)", dialect.MSSQL: "DATETIME2(6)", dialect.SQLite: "DATETIME"}},
		{DateTimeTZ, Options{Precision: 3}, map[dialect.Name]string{dialect.Postgres: "TIMESTAMP(3) WITH TIME ZONE", dialect.MariaDB: "DATETIME(3)", dialect.MSSQL: "DATETIMEOFFSET(3)"}},
		{JSON, Options{}, map[dialect.Name]string{dialect.Postgres: "JSONB", dialect.MySQL: "JSON", dialect.SQLite: "TEXT", dialect.MSSQL: "NVARCHAR(MAX)"}},
		{UUID, Options{}, map[dialect.Name]string{dialect.Postgres: "UUID", dialect.MariaDB: "UUID", dialect.MySQL: "CHAR(36)", dialect.SQLite: "CHAR(36)", dialect.MSSQL: "UNIQUEIDENTIFIER"}},
		{Binary, Options{}, map[dialect.Name]string{dialect.Postgres: "BYTEA", dialect.MySQL: "LONGBLOB", dialect.SQLite: "BLOB", dialect.MSSQL: "VARBINARY(MAX)"}},
		{Binary, Options{Length: 16}, map[dialect.Name]string{dialect.MySQL: "VARBINARY(16)", dialect.MSSQL: "VARBINARY(16)"}},
		{Binary, Options{Length: 16, Fixed: true}, map[dialect.Name]string{dialect.MySQL: "BINARY(16)"}},
		{Blob, Options{Length: 16}, map[dialect.Name]string{dialect.MySQL: "LONGBLOB"}},
	}
	for _, tt := range tests {
		typ := r.MustLookup(tt.name)
		if typ.Name() != tt.name {
			t.Errorf("Name() = %s", typ.Name())
		}
		for d, want := range tt.want {
			if got := typ.SQLDeclaration(d, tt.opts); got != want {
				t.Errorf("%s/%s %+v = %q, want %q", tt.name, d, tt.opts, got, want)
			}
		}
	}
}

type myDecimal struct{ s string }

func (m *myDecimal) Scan(src any) error          { m.s = string(src.([]byte)); return nil }
func (m myDecimal) Value() (driver.Value, error) { return m.s, nil }

func TestInfer(t *testing.T) {
	r := NewRegistry()
	tests := []struct {
		v    any
		want Name
		ok   bool
	}{
		{"", String, true}, {new(string), String, true}, {true, Boolean, true},
		{int8(0), SmallInt, true}, {uint16(0), SmallInt, true}, {int32(0), Integer, true},
		{0, BigInt, true}, {uint64(0), BigInt, true}, {1.5, Float, true}, {float32(0), Float, true},
		{[]byte{}, Binary, true}, {time.Time{}, DateTime, true}, {&time.Time{}, DateTime, true},
		{uuid.UUID{}, UUID, true}, {json.RawMessage{}, JSON, true}, {map[string]any{}, JSON, true},
		{struct{}{}, "", false}, {[]int{}, "", false}, {map[int]int{}, "", false}, {make(chan int), "", false},
	}
	for _, tt := range tests {
		typ, ok := r.Infer(reflect.TypeOf(tt.v))
		if ok != tt.ok || (ok && typ.Name() != tt.want) {
			t.Errorf("Infer(%T) = %v, %v; want %s, %v", tt.v, typ, ok, tt.want, tt.ok)
		}
	}
}

type money int64

type moneyType struct{ Base }

func (moneyType) Name() Name                                  { return "money" }
func (moneyType) SQLDeclaration(dialect.Name, Options) string { return "BIGINT" }

func TestRegisterCustomType(t *testing.T) {
	r := NewRegistry()
	r.Register(moneyType{})
	r.Bind(reflect.TypeFor[money](), "money")
	typ, ok := r.Infer(reflect.TypeFor[*money]())
	if !ok || typ.Name() != "money" {
		t.Fatalf("got %v %v", typ, ok)
	}
	v, err := typ.ToDatabase(money(5), dialect.Postgres)
	if err != nil || v != money(5) {
		t.Fatal("pass-through ToDatabase")
	}
	var m money
	if typ.ScanTarget(&m, dialect.Postgres) != any(&m) {
		t.Fatal("pass-through ScanTarget")
	}
	if _, ok := r.Lookup("nope"); ok {
		t.Fatal("unknown type found")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustLookup should panic")
		}
	}()
	r.MustLookup("nope")
}

func TestIsValuerScanner(t *testing.T) {
	if !IsValuerScanner(reflect.TypeFor[myDecimal]()) || !IsValuerScanner(reflect.TypeFor[uuid.UUID]()) {
		t.Fatal("expected valuer/scanner")
	}
	if IsValuerScanner(reflect.TypeFor[string]()) || IsValuerScanner(reflect.TypeFor[sql.NullString]()) == false {
		t.Fatal("unexpected result")
	}
}

func TestDateTimeToDatabase(t *testing.T) {
	typ := Default.MustLookup(DateTime)
	tz := Default.MustLookup(DateTimeTZ)
	loc := time.FixedZone("X", 2*3600)
	tm := time.Date(2026, 10, 18, 12, 30, 0, 123456000, loc)

	if v, _ := typ.ToDatabase(tm, dialect.SQLite); v != "2026-10-18 10:30:00.123456" {
		t.Errorf("sqlite = %v", v)
	}
	if v, _ := typ.ToDatabase(&tm, dialect.Postgres); v.(time.Time).Location() != time.UTC {
		t.Errorf("datetime should be UTC")
	}
	if v, _ := tz.ToDatabase(tm, dialect.Postgres); v.(time.Time).Location() != loc {
		t.Errorf("datetimetz keeps zone on postgres")
	}
	if v, _ := tz.ToDatabase(tm, dialect.MySQL); v.(time.Time).Location() != time.UTC {
		t.Errorf("datetimetz is UTC on mysql")
	}
	if v, _ := typ.ToDatabase("raw", dialect.Postgres); v != "raw" {
		t.Errorf("non-time passes through")
	}
	var np *time.Time
	if v, _ := typ.ToDatabase(np, dialect.Postgres); v != np {
		t.Errorf("nil pointer passes through: %v", v)
	}
	if v, _ := Default.MustLookup(Date).ToDatabase(tm, dialect.MSSQL); v != "2026-10-18" {
		t.Errorf("date = %v", v)
	}
	if v, _ := Default.MustLookup(Date).ToDatabase(1, dialect.MSSQL); v != 1 {
		t.Errorf("date passthrough = %v", v)
	}
	if v, _ := Default.MustLookup(Time).ToDatabase(tm, dialect.MSSQL); v != "12:30:00.123456" {
		t.Errorf("time = %v", v)
	}
	if v, _ := Default.MustLookup(Time).ToDatabase(1, dialect.MSSQL); v != 1 {
		t.Errorf("time passthrough = %v", v)
	}
}

func TestTimeScanner(t *testing.T) {
	typ := Default.MustLookup(DateTime)
	want := time.Date(2026, 10, 18, 10, 30, 0, 123456000, time.UTC)
	for _, src := range []any{want, "2026-10-18 10:30:00.123456", []byte("2026-10-18T10:30:00.123456Z"), "2026-10-18 10:30:00.123456+00:00"} {
		var got time.Time
		if err := typ.ScanTarget(&got, dialect.SQLite).(sql.Scanner).Scan(src); err != nil || !got.Equal(want) {
			t.Errorf("Scan(%v) = %v, %v", src, got, err)
		}
	}
	var p *time.Time
	if err := typ.ScanTarget(&p, dialect.SQLite).(sql.Scanner).Scan("2026-10-18 10:30:00.123456"); err != nil || p == nil || !p.Equal(want) {
		t.Errorf("pointer scan = %v, %v", p, err)
	}
	if err := typ.ScanTarget(&p, dialect.SQLite).(sql.Scanner).Scan(nil); err != nil || p != nil {
		t.Errorf("nil scan = %v, %v", p, err)
	}
	var d time.Time
	if err := Default.MustLookup(Date).ScanTarget(&d, dialect.SQLite).(sql.Scanner).Scan("2026-10-18"); err != nil || d.Day() != 18 {
		t.Errorf("date scan = %v, %v", d, err)
	}
	var tod time.Time
	if err := Default.MustLookup(Time).ScanTarget(&tod, dialect.SQLite).(sql.Scanner).Scan("12:30:01"); err != nil || tod.Second() != 1 {
		t.Errorf("time scan = %v, %v", tod, err)
	}
	var tm time.Time
	sc := typ.ScanTarget(&tm, dialect.SQLite).(sql.Scanner)
	if err := sc.Scan(42); err == nil {
		t.Error("int should fail")
	}
	if err := sc.Scan("not a time"); err == nil {
		t.Error("garbage should fail")
	}
	var s string
	if err := typ.ScanTarget(&s, dialect.SQLite).(sql.Scanner).Scan(want); err == nil {
		t.Error("string destination should fail")
	}
	if err := typ.ScanTarget(tm, dialect.SQLite).(sql.Scanner).Scan(nil); err == nil {
		t.Error("non-pointer destination should fail")
	}
}

type payload struct {
	A int      `json:"a"`
	B []string `json:"b"`
}

func TestJSON(t *testing.T) {
	typ := Default.MustLookup(JSON)
	v, err := typ.ToDatabase(payload{A: 1, B: []string{"x"}}, dialect.Postgres)
	if err != nil || v != `{"a":1,"b":["x"]}` {
		t.Fatalf("ToDatabase = %v, %v", v, err)
	}
	if v, _ := typ.ToDatabase(json.RawMessage(`[1]`), dialect.Postgres); v != "[1]" {
		t.Fatalf("raw = %v", v)
	}
	// A nil map or slice is a field without pointer: its column is NOT NULL.
	var nilMap map[string]any
	if v, _ := typ.ToDatabase(nilMap, dialect.Postgres); v != "{}" {
		t.Fatalf("nil map = %v", v)
	}
	var nilSlice []string
	if v, _ := typ.ToDatabase(nilSlice, dialect.Postgres); v != "[]" {
		t.Fatalf("nil slice = %v", v)
	}
	var nilPointer *payload
	if v, _ := typ.ToDatabase(nilPointer, dialect.Postgres); v != nil {
		t.Fatalf("nil pointer = %v", v)
	}
	if v, _ := typ.ToDatabase(nil, dialect.Postgres); v != nil {
		t.Fatalf("nil = %v", v)
	}
	if _, err := typ.ToDatabase(make(chan int), dialect.Postgres); err == nil {
		t.Fatal("chan should fail")
	}

	var p payload
	if err := typ.ScanTarget(&p, dialect.Postgres).(sql.Scanner).Scan([]byte(`{"a":2,"b":["y"]}`)); err != nil || p.A != 2 {
		t.Fatalf("scan = %+v, %v", p, err)
	}
	var m map[string]any
	if err := typ.ScanTarget(&m, dialect.SQLite).(sql.Scanner).Scan(`{"k":1}`); err != nil || m["k"] != 1.0 {
		t.Fatalf("scan map = %v, %v", m, err)
	}
	var raw json.RawMessage
	if err := typ.ScanTarget(&raw, dialect.SQLite).(sql.Scanner).Scan(`[1,2]`); err != nil || string(raw) != "[1,2]" {
		t.Fatalf("raw = %s, %v", raw, err)
	}
	if err := typ.ScanTarget(&m, dialect.SQLite).(sql.Scanner).Scan(nil); err != nil || m != nil {
		t.Fatalf("nil = %v, %v", m, err)
	}
	if err := typ.ScanTarget(&m, dialect.SQLite).(sql.Scanner).Scan(1); err == nil {
		t.Fatal("int should fail")
	}
}

func TestUUID(t *testing.T) {
	typ := Default.MustLookup(UUID)
	u := uuid.MustParse("01020304-0506-0708-090a-0b0c0d0e0f10")
	if v, _ := typ.ToDatabase(u, dialect.MSSQL); v != u.String() {
		t.Fatalf("ToDatabase = %v", v)
	}
	if v, _ := typ.ToDatabase(&u, dialect.MSSQL); v != u.String() {
		t.Fatalf("ToDatabase ptr = %v", v)
	}
	var np *uuid.UUID
	if v, _ := typ.ToDatabase(np, dialect.MSSQL); v != nil {
		t.Fatalf("nil ptr = %v", v)
	}
	if v, _ := typ.ToDatabase("x", dialect.MSSQL); v != "x" {
		t.Fatalf("passthrough = %v", v)
	}

	mssqlBytes := []byte{4, 3, 2, 1, 6, 5, 8, 7, 9, 10, 11, 12, 13, 14, 15, 16}
	var got uuid.UUID
	if err := typ.ScanTarget(&got, dialect.MSSQL).(sql.Scanner).Scan(mssqlBytes); err != nil || got != u {
		t.Fatalf("mssql scan = %s, %v", got, err)
	}
	if err := typ.ScanTarget(&got, dialect.Postgres).(sql.Scanner).Scan(u.String()); err != nil || got != u {
		t.Fatalf("pg scan = %s, %v", got, err)
	}
	var p *uuid.UUID
	if err := typ.ScanTarget(&p, dialect.Postgres).(sql.Scanner).Scan(u.String()); err != nil || *p != u {
		t.Fatalf("ptr scan = %v, %v", p, err)
	}
	if err := typ.ScanTarget(&p, dialect.Postgres).(sql.Scanner).Scan("bad"); err == nil {
		t.Fatal("bad ptr scan should fail")
	}
	if err := typ.ScanTarget(&p, dialect.Postgres).(sql.Scanner).Scan(nil); err != nil || p != nil {
		t.Fatalf("nil scan = %v, %v", p, err)
	}
	var s string
	if err := typ.ScanTarget(&s, dialect.Postgres).(sql.Scanner).Scan(u[:]); err != nil || s != u.String() {
		t.Fatalf("string scan = %s, %v", s, err)
	}
	if err := typ.ScanTarget(&s, dialect.Postgres).(sql.Scanner).Scan("bad"); err == nil {
		t.Fatal("bad string scan should fail")
	}
	var ns sql.NullString
	if err := typ.ScanTarget(&ns, dialect.Postgres).(sql.Scanner).Scan(u.String()); err != nil || ns.String != u.String() {
		t.Fatalf("scanner dest = %v, %v", ns, err)
	}
	var i int
	if err := typ.ScanTarget(&i, dialect.Postgres).(sql.Scanner).Scan(u.String()); err == nil {
		t.Fatal("int dest should fail")
	}
}

func TestDeref(t *testing.T) {
	x := 5
	px := &x
	if deref(&px) != 5 || deref(nil) != nil {
		t.Fatal("deref")
	}
}
