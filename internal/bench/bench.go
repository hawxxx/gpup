package bench

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"gpup/internal/engine"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"sync"
	"time"
)

type Config struct {
	TargetID        string  `json:"targetId"`
	Model           string  `json:"model"`
	Prompt          string  `json:"prompt,omitempty"`
	WorkloadHash    string  `json:"workloadHash,omitempty"`
	Concurrency     []int   `json:"concurrency"`
	DurationSeconds float64 `json:"durationSeconds"`
	MaxTokens       int     `json:"maxTokens"`
	// WarmupSeconds runs traffic at each point before measurement starts.
	// Warm-up requests are visible live but excluded from point statistics.
	WarmupSeconds float64 `json:"warmupSeconds,omitempty"`
	// RequestRates switches to an open-loop sweep: requests arrive as a Poisson
	// process at each rate (requests/second), independent of response times.
	// Concurrency then holds exactly one value, the in-flight request cap.
	RequestRates []float64 `json:"requestRates,omitempty"`
	// SLO enables goodput: the share of measured requests meeting every target.
	SLO  *SLO   `json:"slo,omitempty"`
	Name string `json:"name,omitempty"`
}

// SLO holds per-request latency targets in milliseconds; zero disables one.
type SLO struct {
	TTFTMs float64 `json:"ttftMs,omitempty"`
	TPOTMs float64 `json:"tpotMs,omitempty"`
	E2EMs  float64 `json:"e2eMs,omitempty"`
}

// OpenLoop reports whether the sweep varies offered request rate.
func (c Config) OpenLoop() bool { return len(c.RequestRates) > 0 }

func (c Config) Validate() error {
	if c.DurationSeconds <= 0 || c.DurationSeconds > 3600 || math.IsNaN(c.DurationSeconds) || math.IsInf(c.DurationSeconds, 0) {
		return errors.New("durationSeconds must be in (0,3600]")
	}
	if c.WarmupSeconds < 0 || c.WarmupSeconds > 600 || math.IsNaN(c.WarmupSeconds) {
		return errors.New("warmupSeconds must be in [0,600]")
	}
	if c.MaxTokens < 1 || c.MaxTokens > 1048576 {
		return errors.New("maxTokens must be in [1,1048576]")
	}
	if len(c.Concurrency) == 0 || len(c.Concurrency) > 32 {
		return errors.New("provide 1 to 32 concurrency points")
	}
	seen := make(map[int]bool, len(c.Concurrency))
	for _, n := range c.Concurrency {
		if n < 1 || n > 256 {
			return errors.New("concurrency must be in [1,256]")
		}
		if seen[n] {
			return errors.New("concurrency points must be unique")
		}
		seen[n] = true
	}
	if c.OpenLoop() {
		if len(c.Concurrency) != 1 {
			return errors.New("request-rate sweeps take exactly one concurrency value, the in-flight cap")
		}
		if len(c.RequestRates) > 32 {
			return errors.New("provide 1 to 32 request rates")
		}
		seenRate := make(map[float64]bool, len(c.RequestRates))
		for _, r := range c.RequestRates {
			if !(r > 0 && r <= 10000) {
				return errors.New("request rates must be in (0,10000] requests/second")
			}
			if seenRate[r] {
				return errors.New("request rates must be unique")
			}
			seenRate[r] = true
		}
	}
	if c.SLO != nil {
		for _, v := range []float64{c.SLO.TTFTMs, c.SLO.TPOTMs, c.SLO.E2EMs} {
			if !(v >= 0 && v <= 3600000) {
				return errors.New("SLO targets must be in [0,3600000] ms")
			}
		}
		if c.SLO.TTFTMs == 0 && c.SLO.TPOTMs == 0 && c.SLO.E2EMs == 0 {
			return errors.New("SLO requires at least one target")
		}
	}
	if c.Model == "" {
		return errors.New("model required")
	}
	return nil
}

type Percentiles struct {
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Mean float64 `json:"mean"`
	Max  float64 `json:"max"`
}

func Summarize(values []float64) Percentiles {
	if len(values) == 0 {
		return Percentiles{}
	}
	v := append([]float64(nil), values...)
	sort.Float64s(v)
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	get := func(p float64) float64 { return v[int(math.Ceil(p*float64(len(v))))-1] }
	return Percentiles{get(.5), get(.95), get(.99), sum / float64(len(v)), v[len(v)-1]}
}

// LatencySampleLimit bounds each metric's retained latency observations. Above
// this limit, percentiles use an Algorithm R uniform sample of the entire point
// window, rather than only its latest requests. Mean and max remain exact.
const LatencySampleLimit = 10000

type latencyReservoir struct {
	values   []float64
	count    int64
	sum, max float64
	random   *rand.Rand
}

func newLatencyReservoir(seed int64) *latencyReservoir {
	return &latencyReservoir{values: make([]float64, 0, LatencySampleLimit), random: rand.New(rand.NewSource(seed))}
}

// add is serialized by the point mutex; its PRNG is local to one metric/point.
func (r *latencyReservoir) add(v float64) {
	r.count++
	r.sum += v
	if r.count == 1 || v > r.max {
		r.max = v
	}
	if len(r.values) < LatencySampleLimit {
		r.values = append(r.values, v)
		return
	}
	if i := r.random.Int63n(r.count); i < LatencySampleLimit {
		r.values[i] = v
	}
}
func (r *latencyReservoir) summary() Percentiles {
	p := Summarize(r.values)
	if r.count > 0 {
		p.Mean = r.sum / float64(r.count)
		p.Max = r.max
	}
	return p
}

type Finding struct {
	Kind           string   `json:"kind"`
	Severity       string   `json:"severity"`
	Confidence     string   `json:"confidence"`
	Summary        string   `json:"summary"`
	Evidence       []string `json:"evidence"`
	Recommendation string   `json:"recommendation"`
}
type Point struct {
	StartedAt   time.Time `json:"startedAt"`
	EndedAt     time.Time `json:"endedAt"`
	Concurrency int       `json:"concurrency"`
	// RequestRate is the offered Poisson arrival rate of an open-loop point.
	RequestRate *float64 `json:"requestRate,omitempty"`
	Requests    int      `json:"requests"`
	Errors      int      `json:"errors"`
	// ErrorKinds counts measured failures by engine.ErrorKind* class.
	ErrorKinds map[string]int `json:"errorKinds,omitempty"`
	// Incomplete counts measured requests still in flight when the point
	// ended. They are cancelled and excluded from every other statistic.
	Incomplete int `json:"incomplete"`
	// Delayed counts open-loop arrivals that waited for an in-flight slot, so
	// they were sent later than scheduled and the offered rate was not met.
	Delayed int `json:"delayed,omitempty"`
	// Goodput is the fraction of measured requests (errors included) meeting
	// every SLO target; GoodputRPS is those requests per second.
	Goodput               *float64     `json:"goodput,omitempty"`
	GoodputRPS            *float64     `json:"goodputRps,omitempty"`
	DurationSeconds       float64      `json:"durationSeconds"`
	RPS                   float64      `json:"rps"`
	OutputTokensPerSecond *float64     `json:"outputTokensPerSecond,omitempty"`
	TTFT                  *Percentiles `json:"ttft,omitempty"`
	TPOT                  *Percentiles `json:"tpot,omitempty"`
	// ITL summarizes gaps between streamed content chunks. Engines may batch
	// several tokens per chunk, so this is chunk arrival, not per-token latency.
	ITL                *Percentiles `json:"itl,omitempty"`
	E2E                Percentiles  `json:"e2e"`
	GPUSamples         []any        `json:"gpuSamples,omitempty"`
	LatencySampleCount int          `json:"latencySampleCount"`
	LatencySampled     bool         `json:"latencySampled"`
}
type Run struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TargetID  string    `json:"targetId"`
	Model     string    `json:"model"`
	StartedAt time.Time `json:"startedAt"`
	Status    string    `json:"status"`
	Config    Config    `json:"config"`
	Points    []Point   `json:"points"`
	Findings  []Finding `json:"findings"`
	Knee      *int      `json:"knee,omitempty"`
}
type Generator interface {
	Generate(context.Context, string, string, int) (engine.Result, error)
}

func RunBenchmark(ctx context.Context, c Generator, config Config, onResult func(engine.Result)) (Run, error) {
	start := time.Now()
	safe := config
	safe.Prompt = ""
	safe.WorkloadHash = fmt.Sprintf("%x", sha256.Sum256([]byte(config.Prompt)))
	safe.Concurrency = append([]int(nil), config.Concurrency...)
	safe.RequestRates = append([]float64(nil), config.RequestRates...)
	run := Run{ID: strconv.FormatInt(start.UnixNano(), 36), Name: config.Name, TargetID: config.TargetID, Model: config.Model, StartedAt: start, Status: "running", Config: safe, Points: []Point{}, Findings: []Finding{}}
	if e := config.Validate(); e != nil {
		run.Status = "error"
		return run, e
	}
	steps := len(config.Concurrency)
	if config.OpenLoop() {
		steps = len(config.RequestRates)
	}
	for i := 0; i < steps; i++ {
		if e := ctx.Err(); e != nil {
			run.Status = "cancelled"
			return run, e
		}
		n, rate := config.Concurrency[0], 0.0
		if config.OpenLoop() {
			rate = config.RequestRates[i]
		} else {
			n = config.Concurrency[i]
		}
		run.Points = append(run.Points, runPoint(ctx, c, config, n, rate, onResult))
	}
	if e := ctx.Err(); e != nil {
		run.Status = "cancelled"
		return run, e
	}
	run.Status = "complete"
	return run, nil
}

// pointStats accumulates one point's measurements; callers hold mu.
type pointStats struct {
	mu                   sync.Mutex
	p                    Point
	begin                time.Time
	slo                  *SLO
	ttft, tpot, e2e, itl *latencyReservoir
	tokens               int
	usageKnown           bool
	good                 int
	sloUnknown           bool
}

func (s *pointStats) record(issued time.Time, r engine.Result, err error, cancelled bool) {
	// Warm-up traffic is shown live but never measured.
	if issued.Before(s.begin) {
		return
	}
	switch {
	case err != nil && cancelled:
		s.p.Incomplete++
	case err != nil:
		s.p.Requests++
		s.p.Errors++
		kind := r.ErrorKind
		if kind == "" {
			kind = engine.ClassifyError(err)
		}
		if s.p.ErrorKinds == nil {
			s.p.ErrorKinds = map[string]int{}
		}
		s.p.ErrorKinds[kind]++
	default:
		s.p.Requests++
		s.e2e.add(r.DurationMs)
		if r.TTFTMs != nil {
			s.ttft.add(*r.TTFTMs)
		}
		if r.TPOTMs != nil {
			s.tpot.add(*r.TPOTMs)
		}
		for _, gap := range r.ChunkGapsMs {
			s.itl.add(gap)
		}
		if r.OutputTokens == nil {
			s.usageKnown = false
		} else {
			s.tokens += *r.OutputTokens
		}
		if s.slo != nil {
			met, known := s.slo.met(r)
			if !known {
				s.sloUnknown = true
			} else if met {
				s.good++
			}
		}
	}
}

// met reports whether r meets every target; known is false when a targeted
// timing is unavailable, such as TPOT without reported token usage.
func (o SLO) met(r engine.Result) (met, known bool) {
	met = true
	if o.TTFTMs > 0 {
		if r.TTFTMs == nil {
			return false, false
		}
		met = met && *r.TTFTMs <= o.TTFTMs
	}
	if o.TPOTMs > 0 {
		switch {
		case r.TPOTMs != nil:
			met = met && *r.TPOTMs <= o.TPOTMs
		case r.OutputTokens == nil:
			return false, false
		}
		// One generated token has no inter-token time and cannot miss TPOT.
	}
	if o.E2EMs > 0 {
		met = met && r.DurationMs <= o.E2EMs
	}
	return met, true
}

func (s *pointStats) finish(end time.Time) Point {
	p := s.p
	p.EndedAt = end
	p.DurationSeconds = end.Sub(s.begin).Seconds()
	p.RPS = float64(p.Requests-p.Errors) / p.DurationSeconds
	p.E2E = s.e2e.summary()
	p.LatencySampleCount = len(s.e2e.values)
	p.LatencySampled = s.e2e.count > LatencySampleLimit
	for _, m := range []struct {
		r   *latencyReservoir
		out **Percentiles
	}{{s.ttft, &p.TTFT}, {s.tpot, &p.TPOT}, {s.itl, &p.ITL}} {
		if m.r.count > 0 {
			v := m.r.summary()
			*m.out = &v
		}
	}
	if s.usageKnown && s.e2e.count > 0 {
		v := float64(s.tokens) / p.DurationSeconds
		p.OutputTokensPerSecond = &v
	}
	if s.slo != nil && !s.sloUnknown && p.Requests > 0 {
		fraction, rps := float64(s.good)/float64(p.Requests), float64(s.good)/p.DurationSeconds
		p.Goodput, p.GoodputRPS = &fraction, &rps
	}
	return p
}

// runPoint measures one operating point. With rate zero, n closed-loop
// workers each send the next request when the previous one finishes. With a
// positive rate, requests arrive as a Poisson process and n caps how many are
// in flight; arrivals beyond the cap wait for a slot and are counted Delayed.
func runPoint(ctx context.Context, c Generator, config Config, n int, rate float64, onResult func(engine.Result)) Point {
	warmup := time.Duration(config.WarmupSeconds * float64(time.Second))
	begin := time.Now().Add(warmup)
	s := &pointStats{p: Point{Concurrency: n, StartedAt: begin}, begin: begin, slo: config.SLO, usageKnown: true,
		ttft: newLatencyReservoir(1), tpot: newLatencyReservoir(2), e2e: newLatencyReservoir(3), itl: newLatencyReservoir(4)}
	if rate > 0 {
		s.p.RequestRate = &rate
	}
	pointCtx, cancel := context.WithTimeout(ctx, warmup+time.Duration(config.DurationSeconds*float64(time.Second)))
	defer cancel()
	send := func() error {
		issued := time.Now()
		r, e := c.Generate(pointCtx, config.Model, config.Prompt, config.MaxTokens)
		r.Concurrency, r.RequestRate = n, rate
		cancelled := e != nil && pointCtx.Err() != nil
		s.mu.Lock()
		s.record(issued, r, e, cancelled)
		if onResult != nil && !cancelled {
			onResult(r)
		}
		s.mu.Unlock()
		return e
	}
	var wg sync.WaitGroup
	if rate <= 0 {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for pointCtx.Err() == nil {
					if send() != nil {
						// Back off briefly so a failing endpoint is not hammered.
						select {
						case <-pointCtx.Done():
						case <-time.After(10 * time.Millisecond):
						}
					}
				}
			}()
		}
		wg.Wait()
		return s.finish(time.Now())
	}
	// A fixed seed makes the arrival schedule reproducible for a given rate.
	arrivals := rand.New(rand.NewSource(int64(math.Float64bits(rate))))
	slots := make(chan struct{}, n)
	next := time.Now()
	for {
		next = next.Add(time.Duration(arrivals.ExpFloat64() / rate * float64(time.Second)))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-pointCtx.Done():
			timer.Stop()
			wg.Wait()
			return s.finish(time.Now())
		case <-timer.C:
		}
		select {
		case slots <- struct{}{}:
		default:
			if !next.Before(begin) {
				s.mu.Lock()
				s.p.Delayed++
				s.mu.Unlock()
			}
			select {
			case slots <- struct{}{}:
			case <-pointCtx.Done():
				wg.Wait()
				return s.finish(time.Now())
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			_ = send()
		}()
	}
}
