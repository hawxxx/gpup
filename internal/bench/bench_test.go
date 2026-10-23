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

type sleepGenerator struct {
	delay  time.Duration
	active atomic.Int32
	peak   atomic.Int32
}

func (g *sleepGenerator) Generate(ctx context.Context, model, prompt string, max int) (engine.Result, error) {
	n := g.active.Add(1)
	defer g.active.Add(-1)
	for p := g.peak.Load(); p < n && !g.peak.CompareAndSwap(p, n); p = g.peak.Load() {
	}
	select {
	case <-ctx.Done():
		return engine.Result{}, ctx.Err()
	case <-time.After(g.delay):
	}
	ttft, tpot, tokens := 4.0, 2.0, 5
	return engine.Result{DurationMs: float64(g.delay.Milliseconds()), TTFTMs: &ttft, TPOTMs: &tpot, OutputTokens: &tokens}, nil
}

func TestOpenLoopOffersPoissonRate(t *testing.T) {
	run, err := RunBenchmark(context.Background(), &sleepGenerator{delay: time.Millisecond}, Config{Model: "m", Concurrency: []int{64}, RequestRates: []float64{400}, DurationSeconds: 0.5, MaxTokens: 8}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := run.Points[0]
	if p.RequestRate == nil || *p.RequestRate != 400 || p.Delayed != 0 {
		t.Fatalf("open-loop point mislabeled: %+v", p)
	}
	// 200 expected arrivals; a Poisson count stays well within ±30%.
	if p.Requests < 140 || p.Requests > 260 {
		t.Fatalf("achieved %d requests for 200 expected arrivals", p.Requests)
	}
}

func TestOpenLoopCapsInFlightAndCountsDelayedArrivals(t *testing.T) {
	g := &sleepGenerator{delay: 40 * time.Millisecond}
	run, err := RunBenchmark(context.Background(), g, Config{Model: "m", Concurrency: []int{2}, RequestRates: []float64{200}, DurationSeconds: 0.3, MaxTokens: 8}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := run.Points[0]
	if g.peak.Load() > 2 || p.Delayed == 0 || p.RPS >= 200 {
		t.Fatalf("in-flight cap or delay accounting broken: peak %d, %+v", g.peak.Load(), p)
	}
	if p.Incomplete == 0 {
		t.Fatalf("requests cut off by the deadline were not reported: %+v", p)
	}
}

func TestOpenLoopValidation(t *testing.T) {
	base := Config{Model: "m", Concurrency: []int{4}, DurationSeconds: 1, MaxTokens: 1}
	for _, bad := range []Config{
		func() Config { c := base; c.Concurrency = []int{1, 2}; c.RequestRates = []float64{1}; return c }(),
		func() Config { c := base; c.RequestRates = []float64{0}; return c }(),
		func() Config { c := base; c.RequestRates = []float64{2, 2}; return c }(),
		func() Config { c := base; c.SLO = &SLO{}; return c }(),
		func() Config { c := base; c.SLO = &SLO{TTFTMs: -1}; return c }(),
	} {
		if bad.Validate() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

type failingGenerator struct{}

func (failingGenerator) Generate(ctx context.Context, model, prompt string, max int) (engine.Result, error) {
	err := &engine.HTTPError{Status: 503}
	return engine.Result{Status: "error", ErrorKind: engine.ClassifyError(err)}, err
}

func TestErrorKindsAreCounted(t *testing.T) {
	run, _ := RunBenchmark(context.Background(), failingGenerator{}, Config{Model: "m", Concurrency: []int{1}, DurationSeconds: 0.05, MaxTokens: 1}, nil)
	p := run.Points[0]
	if p.Errors == 0 || p.ErrorKinds[engine.ErrorKindHTTP5xx] != p.Errors {
		t.Fatalf("error classes not counted: %+v", p)
	}
}

func TestGoodputAppliesEverySLOTarget(t *testing.T) {
	ttft, fast, slow, one, five := 10.0, 2.0, 9.0, 1, 5
	for _, tc := range []struct {
		slo   SLO
		r     engine.Result
		met   bool
		known bool
	}{
		{SLO{TTFTMs: 20}, engine.Result{TTFTMs: &ttft}, true, true},
		{SLO{TTFTMs: 5}, engine.Result{TTFTMs: &ttft}, false, true},
		{SLO{TTFTMs: 5}, engine.Result{}, false, false},
		{SLO{TPOTMs: 5}, engine.Result{TPOTMs: &fast, OutputTokens: &five}, true, true},
		{SLO{TPOTMs: 5}, engine.Result{TPOTMs: &slow, OutputTokens: &five}, false, true},
		{SLO{TPOTMs: 5}, engine.Result{OutputTokens: &one}, true, true},
		{SLO{TPOTMs: 5}, engine.Result{}, false, false},
		{SLO{E2EMs: 100}, engine.Result{DurationMs: 150}, false, true},
	} {
		if met, known := tc.slo.met(tc.r); met != tc.met || known != tc.known {
			t.Errorf("%+v met=%v known=%v, want %v %v", tc.slo, met, known, tc.met, tc.known)
		}
	}
	run, _ := RunBenchmark(context.Background(), &sleepGenerator{delay: time.Millisecond}, Config{Model: "m", Concurrency: []int{2}, DurationSeconds: 0.05, MaxTokens: 8, SLO: &SLO{TTFTMs: 10, TPOTMs: 1}}, nil)
	if g := run.Points[0].Goodput; g == nil || *g != 0 || run.Points[0].GoodputRPS == nil {
		t.Fatalf("TPOT 2ms must miss a 1ms target: %+v", run.Points[0])
	}
}
