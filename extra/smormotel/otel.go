// Package smormotel traces database queries with OpenTelemetry.
//
//	db.AddQueryHook(smormotel.NewQueryHook())
package smormotel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/internal/brand"
)

// QueryHook creates a client span for every statement.
type QueryHook struct {
	tracer     trace.Tracer
	statements bool
	attrs      []attribute.KeyValue
}

var _ dbal.QueryHook = (*QueryHook)(nil)

// Option configures the hook.
type Option func(*hookConfig)

type hookConfig struct {
	provider   trace.TracerProvider
	statements bool
	attrs      []attribute.KeyValue
}

// WithTracerProvider uses provider instead of the global one.
func WithTracerProvider(p trace.TracerProvider) Option {
	return func(c *hookConfig) { c.provider = p }
}

// WithStatements controls whether the SQL text is recorded (default true;
// bind arguments are never recorded).
func WithStatements(on bool) Option { return func(c *hookConfig) { c.statements = on } }

// WithAttributes adds attributes to every span (e.g. db.namespace).
func WithAttributes(attrs ...attribute.KeyValue) Option {
	return func(c *hookConfig) { c.attrs = append(c.attrs, attrs...) }
}

// NewQueryHook returns a tracing hook.
func NewQueryHook(opts ...Option) *QueryHook {
	c := &hookConfig{statements: true}
	for _, o := range opts {
		o(c)
	}
	if c.provider == nil {
		c.provider = otel.GetTracerProvider()
	}
	return &QueryHook{
		tracer:     c.provider.Tracer("github.com/smatify/" + brand.Name),
		statements: c.statements,
		attrs:      c.attrs,
	}
}

type spanKey struct{}

// System maps a dialect to the OpenTelemetry db.system.name value.
func System(n dialect.Name) string {
	switch n {
	case dialect.Postgres:
		return "postgresql"
	case dialect.MySQL:
		return "mysql"
	case dialect.MariaDB:
		return "mariadb"
	case dialect.SQLite:
		return "sqlite"
	case dialect.MSSQL:
		return "microsoft.sql_server"
	}
	return string(n)
}

// BeforeQuery implements dbal.QueryHook.
func (h *QueryHook) BeforeQuery(ctx context.Context, ev *dbal.QueryEvent) context.Context {
	name := ev.Operation
	if name == "" {
		name = "query"
	}
	attrs := append([]attribute.KeyValue{attribute.String("db.operation.name", ev.Operation)}, h.attrs...)
	if ev.DB != nil {
		attrs = append(attrs, attribute.String("db.system.name", System(ev.DB.Dialect().Name())))
	}
	if h.statements {
		attrs = append(attrs, attribute.String("db.query.text", ev.Query))
	}
	ctx, span := h.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	return context.WithValue(ctx, spanKey{}, span)
}

// AfterQuery implements dbal.QueryHook.
func (h *QueryHook) AfterQuery(ctx context.Context, ev *dbal.QueryEvent) {
	span, ok := ctx.Value(spanKey{}).(trace.Span)
	if !ok {
		return
	}
	if ev.Result != nil {
		if n, err := ev.Result.RowsAffected(); err == nil {
			span.SetAttributes(attribute.Int64("db.response.affected_rows", n))
		}
	}
	if ev.Err != nil {
		span.RecordError(ev.Err)
		span.SetStatus(codes.Error, ev.Err.Error())
	}
	span.End()
}
