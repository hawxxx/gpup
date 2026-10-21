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

type chunkedGenerator struct{}

func (chunkedGenerator) Generate(ctx context.Context, model, prompt string, max int) (engine.Result, error) {
	select {
	case <-ctx.Done():
		return engine.Result{}, ctx.Err()
	case <-time.After(time.Millisecond):
	}
	return engine.Result{DurationMs: 1, ChunkGapsMs: []float64{2, 4}}, nil
}
func TestBenchmarkSummarizesChunkGaps(t *testing.T) {
	run, err := RunBenchmark(context.Background(), chunkedGenerator{}, Config{Model: "m", Concurrency: []int{1}, DurationSeconds: 0.05, MaxTokens: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	itl := run.Points[0].ITL
	if itl == nil || itl.Max != 4 || itl.Mean != 3 {
		t.Fatalf("chunk gaps not summarized: %+v", itl)
	}
}

type clockGenerator struct{}

func (clockGenerator) Generate(ctx context.Context, model, prompt string, max int) (engine.Result, error) {
	start := time.Now()
	select {
	case <-ctx.Done():
		return engine.Result{}, ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}
	return engine.Result{StartedAt: start, DurationMs: 5}, nil
}
func TestWarmupRequestsAreExcluded(t *testing.T) {
	seen := 0
	run, err := RunBenchmark(context.Background(), clockGenerator{}, Config{Model: "m", Concurrency: []int{1}, DurationSeconds: 0.1, WarmupSeconds: 0.1, MaxTokens: 1}, func(engine.Result) { seen++ })
	if err != nil {
		t.Fatal(err)
	}
	p := run.Points[0]
	if p.Requests == 0 || seen <= p.Requests+5 || p.DurationSeconds > 0.15 {
		t.Fatalf("warm-up not excluded: measured %d of %d observed, %.3fs window", p.Requests, seen, p.DurationSeconds)
	}
}
