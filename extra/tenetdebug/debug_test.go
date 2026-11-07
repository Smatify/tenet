package tenetdebug

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smatify/tenet/dbal"
	"github.com/smatify/tenet/internal/brand"
)

func run(h *QueryHook, ev *dbal.QueryEvent) {
	ctx := h.BeforeQuery(context.Background(), ev)
	h.AfterQuery(ctx, ev)
}

func fixedNow(h *QueryHook, start time.Time, d time.Duration) {
	h.now = func() time.Time { return start.Add(d) }
}

func TestVerboseLogsEverything(t *testing.T) {
	var buf bytes.Buffer
	h := NewQueryHook(WithWriter(&buf))
	start := time.Now()
	fixedNow(h, start, time.Millisecond)
	run(h, &dbal.QueryEvent{Query: "SELECT ?", Args: []any{1}, Operation: "SELECT", StartTime: start})
	out := buf.String()
	for _, want := range []string{"SELECT ?", "args=[1]", "1ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q misses %q", out, want)
		}
	}
}

func TestNonVerboseLogsOnlyErrorsAndSlow(t *testing.T) {
	var buf bytes.Buffer
	h := NewQueryHook(WithWriter(&buf), WithVerbose(false), WithSlowThreshold(100*time.Millisecond))
	start := time.Now()
	fixedNow(h, start, time.Millisecond)
	run(h, &dbal.QueryEvent{Query: "fast", StartTime: start})
	if buf.Len() != 0 {
		t.Fatalf("fast query logged: %q", buf.String())
	}
	run(h, &dbal.QueryEvent{Query: "bad", StartTime: start, Err: errors.New("boom")})
	if !strings.Contains(buf.String(), `error="boom"`) {
		t.Fatalf("error not logged: %q", buf.String())
	}
	buf.Reset()
	fixedNow(h, start, time.Second)
	run(h, &dbal.QueryEvent{Query: "slow", StartTime: start})
	if !strings.Contains(buf.String(), "SLOW") {
		t.Fatalf("slow not logged: %q", buf.String())
	}
}

func TestDisabled(t *testing.T) {
	var buf bytes.Buffer
	h := NewQueryHook(WithWriter(&buf), WithEnabled(false))
	run(h, &dbal.QueryEvent{Query: "x", Err: errors.New("e")})
	if buf.Len() != 0 {
		t.Fatal("disabled hook logged")
	}
}

func TestFromEnv(t *testing.T) {
	key := brand.Env("debug")
	cases := []struct {
		val              string
		enabled, verbose bool
	}{{"", false, true}, {"0", false, true}, {"1", true, false}, {"2", true, true}}
	for _, c := range cases {
		t.Setenv(key, c.val)
		h := NewQueryHook(FromEnv())
		if h.enabled != c.enabled || (c.enabled && h.verbose != c.verbose) {
			t.Errorf("%q: enabled=%v verbose=%v", c.val, h.enabled, h.verbose)
		}
	}
}
