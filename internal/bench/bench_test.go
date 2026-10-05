package bench

import (
	"context"
	"gpup/internal/engine"
	"sync/atomic"
	"testing"
	"time"
)

func TestPercentiles(t *testing.T) {
	p := Summarize([]float64{4, 1, 3, 2})
	if p.Mean != 2.5 || p.P50 != 2 || p.P99 != 4 {
		t.Fatalf("bad percentiles %+v", p)
	}
}

type boundedGenerator struct {
	active atomic.Int32
	peak   atomic.Int32
}

func (g *boundedGenerator) Generate(ctx context.Context, model, prompt string, max int) (engine.Result, error) {
	n := g.active.Add(1)
	defer g.active.Add(-1)
	for {
		p := g.peak.Load()
		if p >= n || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return engine.Result{}, ctx.Err()
	case <-time.After(2 * time.Millisecond):
		tokens := 3
		return engine.Result{DurationMs: 2, OutputTokens: &tokens}, nil
	}
}
func TestBoundedSweepAndPromptRedaction(t *testing.T) {
	g := &boundedGenerator{}
	run, e := RunBenchmark(context.Background(), g, Config{Model: "m", Prompt: "secret", Concurrency: []int{3}, DurationSeconds: .025, MaxTokens: 8}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if g.peak.Load() > 3 || g.peak.Load() < 2 {
		t.Fatal("invalid concurrency")
	}
	if run.Config.Prompt != "" || run.Config.WorkloadHash == "" {
		t.Fatal("prompt not redacted")
	}
	if run.Points[0].Errors != 0 || run.Points[0].Requests < 3 {
		t.Fatalf("deadline counted as failure: %+v", run.Points[0])
	}
}
func TestValidation(t *testing.T) {
	if (Config{Concurrency: []int{0}, DurationSeconds: 1, MaxTokens: 1}).Validate() == nil {
		t.Fatal("accepted zero workers")
	}
}

func TestDuplicateConcurrencyRejected(t *testing.T) {
	if (Config{Model: "m", Concurrency: []int{1, 1}, DurationSeconds: 1, MaxTokens: 1}).Validate() == nil {
		t.Fatal("duplicate concurrency must be rejected")
	}
}

func TestLatencyReservoirBoundedAndWholePeriod(t *testing.T) {
	r := newLatencyReservoir(1)
	for i := 1; i <= 100000; i++ {
		r.add(float64(i))
	}
	if len(r.values) != LatencySampleLimit || cap(r.values) > LatencySampleLimit {
		t.Fatalf("unbounded reservoir: len=%d cap=%d", len(r.values), cap(r.values))
	}
	p := r.summary()
	if p.Mean != 50000.5 || p.Max != 100000 {
		t.Fatalf("full population summary lost: %+v", p)
	}
	if p.P50 < 45000 || p.P50 > 55000 {
		t.Fatalf("samples do not represent full window: %+v", p)
	}
	small := newLatencyReservoir(2)
	for _, v := range []float64{4, 1, 3, 2} {
		small.add(v)
	}
	if small.summary() != Summarize([]float64{4, 1, 3, 2}) {
		t.Fatal("small runs must remain exact")
	}
}

type rapidGenerator struct {
	cancel context.CancelFunc
	count  int
}

func (g *rapidGenerator) Generate(ctx context.Context, model, prompt string, max int) (engine.Result, error) {
	g.count++
	if g.count > 30000 {
		g.cancel()
		return engine.Result{}, context.Canceled
	}
	v := float64(g.count)
	return engine.Result{DurationMs: v, TTFTMs: &v, TPOTMs: &v}, nil
}
func TestRapidBenchmarkReportsSampling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := &rapidGenerator{cancel: cancel}
	run, e := RunBenchmark(ctx, g, Config{Model: "m", Concurrency: []int{1}, DurationSeconds: 2, MaxTokens: 1}, nil)
	if e != context.Canceled {
		t.Fatalf("expected cancellation: %v", e)
	}
	p := run.Points[0]
	if p.Requests != 30000 || p.LatencySampleCount != LatencySampleLimit || !p.LatencySampled {
		t.Fatalf("sampling metadata invalid: %+v", p)
	}
	if p.E2E.Mean != 15000.5 || p.E2E.Max != 30000 {
		t.Fatal("exact aggregate lost")
	}
}
