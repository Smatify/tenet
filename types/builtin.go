package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/uuid"
)

func builtins() []Type {
	return []Type{
		stringType{}, textType{}, intType{name: SmallInt}, intType{name: Integer}, intType{name: BigInt},
		boolType{}, decimalType{}, floatType{}, dateType{}, timeType{}, dateTimeType{tz: false},
		dateTimeType{tz: true}, jsonType{}, uuidType{}, binaryType{name: Binary}, binaryType{name: Blob},
	}
}

func defaultBindings() map[reflect.Type]Name {
	return map[reflect.Type]Name{
		reflect.TypeFor[time.Time]():       DateTime,
		reflect.TypeFor[uuid.UUID]():       UUID,
		reflect.TypeFor[json.RawMessage](): JSON,
		reflect.TypeFor[[]byte]():          Binary,
	}
}

func isMySQL(d dialect.Name) bool { return d == dialect.MySQL || d == dialect.MariaDB }

func or(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

type stringType struct{ Base }

func (stringType) Name() Name { return String }
func (stringType) SQLDeclaration(d dialect.Name, o Options) string {
	n := strconv.Itoa(or(o.Length, 255))
	switch {
	case d == dialect.MSSQL && o.Fixed:
		return "NCHAR(" + n + ")"
	case d == dialect.MSSQL:
		return "NVARCHAR(" + n + ")"
	case o.Fixed:
		return "CHAR(" + n + ")"
	default:
		return "VARCHAR(" + n + ")"
	}
}

type textType struct{ Base }

func (textType) Name() Name { return Text }
func (textType) SQLDeclaration(d dialect.Name, _ Options) string {
	switch {
	case isMySQL(d):
		return "LONGTEXT"
	case d == dialect.MSSQL:
		return "NVARCHAR(MAX)"
	default:
		return "TEXT"
	}
}

type intType struct {
	Base
	name Name
}

func (t intType) Name() Name { return t.name }
func (t intType) SQLDeclaration(d dialect.Name, _ Options) string {
	switch t.name {
	case SmallInt:
		return "SMALLINT"
	case Integer:
		if d == dialect.MSSQL || isMySQL(d) {
			return "INT"
		}
		return "INTEGER"
	default:
		return "BIGINT"
	}
}

type boolType struct{ Base }

func (boolType) Name() Name { return Boolean }
func (boolType) SQLDeclaration(d dialect.Name, _ Options) string {
	switch {
	case isMySQL(d):
		return "TINYINT(1)"
	case d == dialect.MSSQL:
		return "BIT"
	default:
		return "BOOLEAN"
	}
}

type decimalType struct{ Base }

func (decimalType) Name() Name { return Decimal }
func (decimalType) SQLDeclaration(d dialect.Name, o Options) string {
	p, s := or(o.Precision, 10), o.Scale
	kw := "DECIMAL"
	if d == dialect.Postgres {
		kw = "NUMERIC"
	}
	return fmt.Sprintf("%s(%d, %d)", kw, p, s)
}

type floatType struct{ Base }

func (floatType) Name() Name { return Float }
func (floatType) SQLDeclaration(d dialect.Name, _ Options) string {
	switch d {
	case dialect.Postgres:
		return "DOUBLE PRECISION"
	case dialect.SQLite:
		return "REAL"
	case dialect.MSSQL:
		return "FLOAT"
	default:
		return "DOUBLE"
	}
}

type dateType struct{}

func (dateType) Name() Name                                  { return Date }
func (dateType) SQLDeclaration(dialect.Name, Options) string { return "DATE" }

// ToDatabase sends dates as YYYY-MM-DD so no driver applies a time zone shift.
func (dateType) ToDatabase(v any, _ dialect.Name) (any, error) {
	if t, ok := deref(v).(time.Time); ok {
		return t.Format(time.DateOnly), nil
	}
	return v, nil
}
func (dateType) ScanTarget(dest any, _ dialect.Name) any {
	return &timeScanner{dest: dest, layout: time.DateOnly}
}

type timeType struct{}

func (timeType) Name() Name { return Time }
func (timeType) SQLDeclaration(d dialect.Name, _ Options) string {
	if isMySQL(d) || d == dialect.Postgres {
		return "TIME(6)"
	}
	return "TIME"
}

// ToDatabase sends times of day as HH:MM:SS.ffffff strings.
func (timeType) ToDatabase(v any, _ dialect.Name) (any, error) {
	if t, ok := deref(v).(time.Time); ok {
		return t.Format("15:04:05.999999"), nil
	}
	return v, nil
}
func (timeType) ScanTarget(dest any, _ dialect.Name) any {
	return &timeScanner{dest: dest, layout: "15:04:05.999999999"}
}

type dateTimeType struct{ tz bool }

func (t dateTimeType) Name() Name {
	if t.tz {
		return DateTimeTZ
	}
	return DateTime
}

func (t dateTimeType) SQLDeclaration(d dialect.Name, o Options) string {
	p := strconv.Itoa(or(o.Precision, 6))
	switch {
	case d == dialect.Postgres && t.tz:
		return "TIMESTAMP(" + p + ") WITH TIME ZONE"
	case d == dialect.Postgres:
		return "TIMESTAMP(" + p + ") WITHOUT TIME ZONE"
	case isMySQL(d):
		return "DATETIME(" + p + ")"
	case d == dialect.MSSQL && t.tz:
		return "DATETIMEOFFSET(" + p + ")"
	case d == dialect.MSSQL:
		return "DATETIME2(" + p + ")"
	default:
		return "DATETIME"
	}
}

// ToDatabase normalizes to UTC for databases without time zone support.
func (t dateTimeType) ToDatabase(v any, d dialect.Name) (any, error) {
	tm, ok := deref(v).(time.Time)
	if !ok {
		return v, nil
	}
	if d == dialect.SQLite {
		return tm.UTC().Format("2006-01-02 15:04:05.999999999"), nil
	}
	if !t.tz || isMySQL(d) {
		return tm.UTC(), nil
	}
	return tm, nil
}

func (dateTimeType) ScanTarget(dest any, _ dialect.Name) any {
	return &timeScanner{dest: dest, layout: "2006-01-02 15:04:05.999999999"}
}

// timeScanner accepts time.Time, strings and byte slices (SQLite, MySQL
// without parseTime) and stores the result in *time.Time or **time.Time.
type timeScanner struct {
	dest   any
	layout string
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", time.DateOnly, "15:04:05.999999999"}

func (s *timeScanner) Scan(src any) error {
	var t time.Time
	switch v := src.(type) {
	case nil:
		return setNil(s.dest)
	case time.Time:
		t = v
	case []byte:
		return s.Scan(string(v))
	case string:
		var err error
		if t, err = parseTime(v, s.layout); err != nil {
			return err
		}
	default:
		return fmt.Errorf("types: cannot scan %T into time", src)
	}
	switch d := s.dest.(type) {
	case *time.Time:
		*d = t
	case **time.Time:
		*d = &t
	default:
		return fmt.Errorf("types: unsupported time destination %T", s.dest)
	}
	return nil
}

func parseTime(v, preferred string) (time.Time, error) {
	if t, err := time.ParseInLocation(preferred, v, time.UTC); err == nil {
		return t, nil
	}
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, v, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("types: cannot parse time %q", v)
}

type jsonType struct{}

func (jsonType) Name() Name { return JSON }
func (jsonType) SQLDeclaration(d dialect.Name, _ Options) string {
	switch {
	case d == dialect.Postgres:
		return "JSONB"
	case isMySQL(d):
		return "JSON"
	case d == dialect.MSSQL:
		return "NVARCHAR(MAX)"
	default:
		return "TEXT"
	}
}

// ToDatabase marshals v to a JSON string. A nil pointer becomes NULL (a
// pointer field is the nullable column); a nil slice or map of a field
// without pointer becomes [] or {}, its column is NOT NULL.
func (jsonType) ToDatabase(v any, _ dialect.Name) (any, error) {
	if v != nil {
		switch rv := reflect.ValueOf(v); {
		case rv.Kind() == reflect.Slice && rv.IsNil():
			return "[]", nil
		case rv.Kind() == reflect.Map && rv.IsNil():
			return "{}", nil
		}
	}
	if isNil(v) {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return string(raw), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
func (jsonType) ScanTarget(dest any, _ dialect.Name) any { return &jsonScanner{dest: dest} }

type jsonScanner struct{ dest any }

func (s *jsonScanner) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		return setNil(s.dest)
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("types: cannot scan %T into json", src)
	}
	if raw, ok := s.dest.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], b...)
		return nil
	}
	return json.Unmarshal(b, s.dest)
}

type uuidType struct{}

func (uuidType) Name() Name { return UUID }
func (uuidType) SQLDeclaration(d dialect.Name, _ Options) string {
	switch d {
	case dialect.Postgres, dialect.MariaDB:
		return "UUID"
	case dialect.MSSQL:
		return "UNIQUEIDENTIFIER"
	default:
		return "CHAR(36)"
	}
}
func (uuidType) ToDatabase(v any, _ dialect.Name) (any, error) {
	switch u := deref(v).(type) {
	case uuid.UUID:
		return u.String(), nil
	case nil:
		return nil, nil
	}
	return v, nil
}
func (uuidType) ScanTarget(dest any, d dialect.Name) any {
	return &uuidScanner{dest: dest, mssql: d == dialect.MSSQL}
}

// uuidScanner fixes SQL Server's mixed-endian UNIQUEIDENTIFIER byte order.
type uuidScanner struct {
	dest  any
	mssql bool
}

func (s *uuidScanner) Scan(src any) error {
	if src == nil {
		return setNil(s.dest)
	}
	if b, ok := src.([]byte); ok && len(b) == 16 && s.mssql {
		b = []byte{b[3], b[2], b[1], b[0], b[5], b[4], b[7], b[6], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15]}
		src = b
	}
	switch d := s.dest.(type) {
	case *uuid.UUID:
		return d.Scan(src)
	case **uuid.UUID:
		u := new(uuid.UUID)
		if err := u.Scan(src); err != nil {
			return err
		}
		*d = u
		return nil
	case *string:
		var u uuid.UUID
		if err := u.Scan(src); err != nil {
			return err
		}
		*d = u.String()
		return nil
	}
	if sc, ok := s.dest.(interface{ Scan(any) error }); ok {
		return sc.Scan(src)
	}
	return fmt.Errorf("types: unsupported uuid destination %T", s.dest)
}

type binaryType struct {
	Base
	name Name
}

func (t binaryType) Name() Name { return t.name }
func (t binaryType) SQLDeclaration(d dialect.Name, o Options) string {
	large := t.name == Blob || o.Length == 0
	switch {
	case d == dialect.Postgres:
		return "BYTEA"
	case d == dialect.SQLite:
		return "BLOB"
	case isMySQL(d) && large:
		return "LONGBLOB"
	case d == dialect.MSSQL && large:
		return "VARBINARY(MAX)"
	case o.Fixed:
		return "BINARY(" + strconv.Itoa(o.Length) + ")"
	default:
		return "VARBINARY(" + strconv.Itoa(o.Length) + ")"
	}
}

func deref(v any) any {
	rv := reflect.ValueOf(v)
	for rv.IsValid() && rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil
	}
	return rv.Interface()
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

var errNotPointer = errors.New("types: scan destination must be a pointer")

// setNil sets the value dest points to to its zero value (nil for pointers).
func setNil(dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errNotPointer
	}
	rv.Elem().SetZero()
	return nil
}
