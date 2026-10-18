// Package smormdebug provides a query hook that logs statements for debugging.
package smormdebug

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/smatify/smorm/dbal"
	"github.com/smatify/smorm/internal/brand"
)

// QueryHook logs queries to a writer.
type QueryHook struct {
	enabled bool
	verbose bool // log every query, not only failures and slow ones
	slow    time.Duration
	w       io.Writer
	now     func() time.Time
}

var _ dbal.QueryHook = (*QueryHook)(nil)

// Option configures the hook.
type Option func(*QueryHook)

// WithEnabled enables or disables logging.
func WithEnabled(on bool) Option { return func(h *QueryHook) { h.enabled = on } }

// WithVerbose logs every query instead of only failed and slow ones.
func WithVerbose(on bool) Option { return func(h *QueryHook) { h.verbose = on } }

// WithSlowThreshold logs queries slower than d even when not verbose.
func WithSlowThreshold(d time.Duration) Option { return func(h *QueryHook) { h.slow = d } }

// WithWriter sets the output (default os.Stderr).
func WithWriter(w io.Writer) Option { return func(h *QueryHook) { h.w = w } }

// FromEnv configures the hook from the <PREFIX>DEBUG environment variable:
// "0" or empty disables it, "1" logs failed/slow queries, "2" logs everything.
func FromEnv() Option {
	return func(h *QueryHook) {
		switch os.Getenv(brand.Env("debug")) {
		case "", "0":
			h.enabled = false
		case "1":
			h.enabled, h.verbose = true, false
		default:
			h.enabled, h.verbose = true, true
		}
	}
}

// NewQueryHook returns a new hook. By default it is enabled and verbose.
func NewQueryHook(opts ...Option) *QueryHook {
	h := &QueryHook{enabled: true, verbose: true, w: os.Stderr, now: time.Now}
	for _, o := range opts {
		o(h)
	}
	return h
}

// BeforeQuery implements dbal.QueryHook.
func (h *QueryHook) BeforeQuery(ctx context.Context, _ *dbal.QueryEvent) context.Context { return ctx }

// AfterQuery implements dbal.QueryHook.
func (h *QueryHook) AfterQuery(_ context.Context, ev *dbal.QueryEvent) {
	if !h.enabled {
		return
	}
	dur := h.now().Sub(ev.StartTime)
	isSlow := h.slow > 0 && dur >= h.slow
	if !h.verbose && ev.Err == nil && !isSlow {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %-8s %10s  %s", brand.Name, ev.Operation, dur.Round(time.Microsecond), ev.Query)
	if len(ev.Args) > 0 {
		fmt.Fprintf(&b, "  args=%v", ev.Args)
	}
	if isSlow {
		b.WriteString("  SLOW")
	}
	if ev.Err != nil {
		fmt.Fprintf(&b, "  error=%q", ev.Err.Error())
	}
	b.WriteByte('\n')
	_, _ = io.WriteString(h.w, b.String())
}
