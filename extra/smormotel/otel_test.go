package smormotel

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/fakedb"
	"github.com/smatify/smorm/internal/testdialect"
)

func setup(t *testing.T, opts ...Option) (*dbal.DB, *fakedb.Recorder, *tracetest.SpanRecorder) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	rec := fakedb.New()
	db := dbal.NewDB(rec.DB(), testdialect.Like(dialect.Postgres))
	db.AddQueryHook(NewQueryHook(append([]Option{WithTracerProvider(tp)}, opts...)...))
	t.Cleanup(func() { _ = db.Close() })
	return db, rec, sr
}

func attr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, a := range s.Attributes() {
		if string(a.Key) == key {
			return a.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestSpans(t *testing.T) {
	db, rec, sr := setup(t, WithAttributes(attribute.String("db.namespace", "app")))
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES ($1)", 1); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	rec.FailOn("SELECT", boom)
	_, _ = db.QueryContext(ctx, "SELECT 1")
	spans := sr.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans = %d", len(spans))
	}
	ins := spans[0]
	if ins.Name() != "INSERT" || ins.SpanKind() != trace.SpanKindClient {
		t.Fatalf("span = %s %v", ins.Name(), ins.SpanKind())
	}
	for key, want := range map[string]string{"db.system.name": "postgresql", "db.query.text": "INSERT INTO t VALUES ($1)", "db.operation.name": "INSERT", "db.namespace": "app"} {
		if v, ok := attr(ins, key); !ok || v.AsString() != want {
			t.Errorf("%s = %v", key, v.AsString())
		}
	}
	if v, ok := attr(ins, "db.response.affected_rows"); !ok || v.AsInt64() != 1 {
		t.Error("affected rows")
	}
	if spans[1].Status().Code != codes.Error || len(spans[1].Events()) == 0 {
		t.Fatal("errors must be recorded")
	}
}

func TestWithoutStatements(t *testing.T) {
	db, _, sr := setup(t, WithStatements(false))
	_, _ = db.ExecContext(context.Background(), "DELETE FROM secret")
	if _, ok := attr(sr.Ended()[0], "db.query.text"); ok {
		t.Fatal("statement recorded")
	}
}

func TestHelpers(t *testing.T) {
	for n, want := range map[dialect.Name]string{dialect.MySQL: "mysql", dialect.MariaDB: "mariadb", dialect.SQLite: "sqlite",
		dialect.MSSQL: "microsoft.sql_server", "other": "other"} {
		if System(n) != want {
			t.Errorf("System(%s)", n)
		}
	}
	h := NewQueryHook()
	h.AfterQuery(context.Background(), &dbal.QueryEvent{}) // no span: no panic
	ctx := h.BeforeQuery(context.Background(), &dbal.QueryEvent{})
	h.AfterQuery(ctx, &dbal.QueryEvent{})
}
