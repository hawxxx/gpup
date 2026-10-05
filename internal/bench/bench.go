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
	Name            string  `json:"name,omitempty"`
}

func (c Config) Validate() error {
	if c.DurationSeconds <= 0 || c.DurationSeconds > 3600 || math.IsNaN(c.DurationSeconds) || math.IsInf(c.DurationSeconds, 0) {
		return errors.New("durationSeconds must be in (0,3600]")
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
	StartedAt             time.Time    `json:"startedAt"`
	EndedAt               time.Time    `json:"endedAt"`
	Concurrency           int          `json:"concurrency"`
	Requests              int          `json:"requests"`
	Errors                int          `json:"errors"`
	DurationSeconds       float64      `json:"durationSeconds"`
	RPS                   float64      `json:"rps"`
	OutputTokensPerSecond *float64     `json:"outputTokensPerSecond,omitempty"`
	TTFT                  *Percentiles `json:"ttft,omitempty"`
	TPOT                  *Percentiles `json:"tpot,omitempty"`
	E2E                   Percentiles  `json:"e2e"`
	GPUSamples            []any        `json:"gpuSamples,omitempty"`
	LatencySampleCount    int          `json:"latencySampleCount"`
	LatencySampled        bool         `json:"latencySampled"`
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
	run := Run{ID: strconv.FormatInt(start.UnixNano(), 36), Name: config.Name, TargetID: config.TargetID, Model: config.Model, StartedAt: start, Status: "running", Config: safe, Points: []Point{}, Findings: []Finding{}}
	if e := config.Validate(); e != nil {
		run.Status = "error"
		return run, e
	}
	for _, n := range config.Concurrency {
		if e := ctx.Err(); e != nil {
			run.Status = "cancelled"
			return run, e
		}
		p := Point{Concurrency: n}
		begin := time.Now()
		p.StartedAt = begin
		pointCtx, cancel := context.WithTimeout(ctx, time.Duration(config.DurationSeconds*float64(time.Second)))
		var mu sync.Mutex
		var wg sync.WaitGroup
		ttft, tpot, e2e := newLatencyReservoir(1), newLatencyReservoir(2), newLatencyReservoir(3)
		tokens := 0
		known := true
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for pointCtx.Err() == nil {
					r, e := c.Generate(pointCtx, config.Model, config.Prompt, config.MaxTokens)
					r.Concurrency = n
					if e != nil && pointCtx.Err() != nil {
						return
					}
					mu.Lock()
					p.Requests++
					if e != nil {
						p.Errors++
					} else {
						e2e.add(r.DurationMs)
						if r.TTFTMs != nil {
							ttft.add(*r.TTFTMs)
						}
						if r.TPOTMs != nil {
							tpot.add(*r.TPOTMs)
						}
						if r.OutputTokens == nil {
							known = false
						} else {
							tokens += *r.OutputTokens
						}
					}
					if onResult != nil {
						onResult(r)
					}
					mu.Unlock()
					if e != nil {
						select {
						case <-pointCtx.Done():
							return
						case <-time.After(10 * time.Millisecond):
						}
					}
				}
			}()
		}
		wg.Wait()
		cancel()
		p.EndedAt = time.Now()
		p.DurationSeconds = p.EndedAt.Sub(begin).Seconds()
		p.RPS = float64(p.Requests-p.Errors) / p.DurationSeconds
		p.E2E = e2e.summary()
		p.LatencySampleCount = len(e2e.values)
		p.LatencySampled = e2e.count > LatencySampleLimit
		if ttft.count > 0 {
			v := ttft.summary()
			p.TTFT = &v
		}
		if tpot.count > 0 {
			v := tpot.summary()
			p.TPOT = &v
		}
		if known && e2e.count > 0 {
			v := float64(tokens) / p.DurationSeconds
			p.OutputTokensPerSecond = &v
		}
		run.Points = append(run.Points, p)
	}
	if e := ctx.Err(); e != nil {
		run.Status = "cancelled"
		return run, e
	}
	run.Status = "complete"
	return run, nil
}
