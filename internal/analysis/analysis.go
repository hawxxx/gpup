package analysis

import (
	"errors"
	"fmt"
	"gpup/internal/bench"
	"math"
	"sort"
)

type Observation struct {
	GPUUtilization          []float64
	KVCachePercent          *float64
	RequestsWaiting         *float64
	Evictions               *float64
	MemoryControllerPercent []float64
	TemperatureC            []float64
	Throttled               []bool
	RequestRate             *float64
	ThroughputGainPercent   *float64
}

func Findings(o Observation) []bench.Finding {
	out := []bench.Finding{}
	add := func(kind, summary, evidence, recommendation string) {
		out = append(out, bench.Finding{Kind: kind, Severity: "warning", Confidence: "medium", Summary: summary, Evidence: []string{evidence}, Recommendation: recommendation})
	}
	mean := func(v []float64) (float64, bool) {
		if len(v) == 0 {
			return 0, false
		}
		sum := 0.0
		for _, n := range v {
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
				return 0, false
			}
			sum += n
		}
		return sum / float64(len(v)), true
	}
	util, hasUtil := mean(o.GPUUtilization)
	memory, hasMemory := mean(o.MemoryControllerPercent)
	active := o.RequestRate != nil && *o.RequestRate > 0
	flat := o.ThroughputGainPercent != nil && *o.ThroughputGainPercent >= 0 && *o.ThroughputGainPercent < 10
	if active && flat && hasUtil && hasMemory {
		if util >= 90 && memory < 70 {
			add("compute-bound", "Evidence is consistent with compute saturation", fmt.Sprintf("GPU activity %.1f%%, memory-controller activity %.1f%%, throughput gain %.1f%%", util, memory, *o.ThroughputGainPercent), "Profile kernel activity before changing GPU count or quantization.")
		}
		if memory >= 90 && util < 90 {
			add("memory-bandwidth-bound", "Evidence is consistent with memory bandwidth pressure", fmt.Sprintf("Memory-controller activity %.1f%%, GPU activity %.1f%%, throughput gain %.1f%%", memory, util, *o.ThroughputGainPercent), "Profile memory traffic and consider batching or quantization; controller activity is not a bandwidth measurement.")
		}
	}
	if active && hasUtil && util < 20 && o.RequestsWaiting != nil && *o.RequestsWaiting == 0 {
		add("underutilized", "GPU activity is low under the observed request load", fmt.Sprintf("GPU activity %.1f%%, request rate %.2f/s, no queued requests", util, *o.RequestRate), "Increase offered concurrency carefully and compare token throughput and latency.")
	}
	for i, throttled := range o.Throttled {
		if throttled && i < len(o.TemperatureC) && o.TemperatureC[i] >= 85 {
			add("thermal-pressure", "High temperature coincides with GPU throttling", fmt.Sprintf("GPU sample %d: %.1f C and throttling reported", i, o.TemperatureC[i]), "Inspect cooling and throttle reasons; this observation does not prove thermal throttling.")
		}
	}
	if len(o.GPUUtilization) > 1 {
		lo, hi := o.GPUUtilization[0], o.GPUUtilization[0]
		for _, v := range o.GPUUtilization {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		if hi >= 50 && hi-lo >= 30 {
			out = append(out, bench.Finding{Kind: "gpu-imbalance", Severity: "warning", Confidence: "medium", Summary: "GPU utilization is uneven", Evidence: []string{fmt.Sprintf("Observed utilization ranges from %.1f%% to %.1f%%", lo, hi)}, Recommendation: "Check tensor parallel placement and workload distribution; a single snapshot does not establish a persistent bottleneck."})
		}
	}
	if o.KVCachePercent != nil && *o.KVCachePercent >= 90 && o.RequestsWaiting != nil && *o.RequestsWaiting > 0 {
		out = append(out, bench.Finding{Kind: "cache-pressure", Severity: "warning", Confidence: "medium", Summary: "High KV cache occupancy coincides with queued requests", Evidence: []string{fmt.Sprintf("KV cache %.1f%%; waiting requests %.0f", *o.KVCachePercent, *o.RequestsWaiting)}, Recommendation: "Inspect context lengths, cache eviction counters, and memory headroom before changing capacity."})
	}
	return out
}
func DetectKnee(points []bench.Point) *int {
	p := append([]bench.Point(nil), points...)
	sort.Slice(p, func(i, j int) bool { return p[i].Concurrency < p[j].Concurrency })
	for i := 1; i < len(p); i++ {
		a, b := p[i-1], p[i]
		before, after := a.RPS, b.RPS
		if a.OutputTokensPerSecond != nil && b.OutputTokensPerSecond != nil {
			before, after = *a.OutputTokensPerSecond, *b.OutputTokensPerSecond
		}
		if a.Errors == 0 && b.Errors == 0 && before > 0 && after > 0 && b.Concurrency > a.Concurrency && a.E2E.P95 > 0 && after/before < 1.1 && b.E2E.P95/a.E2E.P95 > 1.2 {
			n := a.Concurrency
			return &n
		}
	}
	return nil
}

type Change struct {
	Concurrency                  int      `json:"concurrency"`
	RPSPercent                   float64  `json:"rpsPercent"`
	LatencyP95Percent            float64  `json:"latencyP95Percent"`
	OutputTokensPerSecondPercent *float64 `json:"outputTokensPerSecondPercent,omitempty"`
	TTFTP99Percent               *float64 `json:"ttftP99Percent,omitempty"`
	ThroughputMetric             string   `json:"throughputMetric"`
}
type Comparison struct {
	BaselineID  string   `json:"baselineId"`
	CandidateID string   `json:"candidateId"`
	Changes     []Change `json:"changes"`
	Compatible  bool     `json:"compatible"`
}

func Compare(a, b bench.Run) (Comparison, error) {
	out := Comparison{BaselineID: a.ID, CandidateID: b.ID, Changes: []Change{}}
	if a.Model != b.Model || a.Config.MaxTokens != b.Config.MaxTokens || a.Config.DurationSeconds != b.Config.DurationSeconds || a.Config.WorkloadHash != b.Config.WorkloadHash {
		return out, errors.New("incompatible workload: model, max tokens, and duration must match")
	}
	if a.Status != "complete" || b.Status != "complete" {
		return out, errors.New("only completed runs can be compared")
	}
	if len(a.Points) != len(b.Points) || len(a.Points) == 0 {
		return out, errors.New("incompatible concurrency sweep")
	}
	for _, run := range []bench.Run{a, b} {
		seen := map[int]bool{}
		for _, point := range run.Points {
			if seen[point.Concurrency] {
				return out, errors.New("duplicate concurrency points")
			}
			seen[point.Concurrency] = true
			if point.Errors > 0 {
				return out, errors.New("errorful benchmark points cannot be compared")
			}
		}
	}
	for _, p := range a.Points {
		var q *bench.Point
		for i := range b.Points {
			if b.Points[i].Concurrency == p.Concurrency {
				q = &b.Points[i]
				break
			}
		}
		if q == nil || p.RPS <= 0 || p.E2E.P95 <= 0 || q.RPS <= 0 || q.E2E.P95 <= 0 {
			return out, errors.New("comparison requires matching measured successful points")
		}
		change := Change{Concurrency: p.Concurrency, RPSPercent: (q.RPS/p.RPS - 1) * 100, LatencyP95Percent: (q.E2E.P95/p.E2E.P95 - 1) * 100, ThroughputMetric: "rps"}
		if p.OutputTokensPerSecond != nil && q.OutputTokensPerSecond != nil && *p.OutputTokensPerSecond > 0 {
			v := (*q.OutputTokensPerSecond / *p.OutputTokensPerSecond - 1) * 100
			change.OutputTokensPerSecondPercent = &v
			change.ThroughputMetric = "outputTokensPerSecond"
		}
		if p.TTFT != nil && q.TTFT != nil && p.TTFT.P99 > 0 {
			v := (q.TTFT.P99/p.TTFT.P99 - 1) * 100
			change.TTFTP99Percent = &v
		}
		out.Changes = append(out.Changes, change)
	}
	out.Compatible = true
	return out, nil
}
func CheckThresholds(c Comparison, maxThroughputDropPercent, maxLatencyIncreasePercent float64) error {
	if !c.Compatible || len(c.Changes) == 0 {
		return errors.New("invalid comparison")
	}
	if maxThroughputDropPercent < 0 || maxLatencyIncreasePercent < 0 || math.IsNaN(maxThroughputDropPercent) || math.IsNaN(maxLatencyIncreasePercent) || math.IsInf(maxThroughputDropPercent, 0) || math.IsInf(maxLatencyIncreasePercent, 0) {
		return errors.New("thresholds must be nonnegative")
	}
	for _, p := range c.Changes {
		throughput := p.RPSPercent
		metric := "rps"
		if p.OutputTokensPerSecondPercent != nil {
			throughput = *p.OutputTokensPerSecondPercent
			metric = "outputTokensPerSecond"
		}
		if math.IsNaN(p.RPSPercent) || math.IsNaN(p.LatencyP95Percent) || math.IsInf(p.RPSPercent, 0) || math.IsInf(p.LatencyP95Percent, 0) {
			return errors.New("comparison contains nonfinite measurements")
		}
		if math.IsNaN(throughput) || math.IsInf(throughput, 0) {
			return errors.New("comparison contains nonfinite throughput")
		}
		if throughput < -maxThroughputDropPercent || p.LatencyP95Percent > maxLatencyIncreasePercent {
			return fmt.Errorf("regression at concurrency %d: %s %.1f%%, p95 latency %.1f%%", p.Concurrency, metric, throughput, p.LatencyP95Percent)
		}
	}
	return nil
}

func CheckTTFTP99Threshold(c Comparison, maxIncreasePercent float64) error {
	if !c.Compatible || len(c.Changes) == 0 || maxIncreasePercent < 0 || math.IsNaN(maxIncreasePercent) || math.IsInf(maxIncreasePercent, 0) {
		return errors.New("invalid comparison or threshold")
	}
	for _, p := range c.Changes {
		if p.TTFTP99Percent == nil {
			return errors.New("TTFT p99 unavailable for comparison")
		}
		v := *p.TTFTP99Percent
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("nonfinite TTFT p99")
		}
		if v > maxIncreasePercent {
			return fmt.Errorf("TTFT p99 regression at concurrency %d: %.1f%%", p.Concurrency, v)
		}
	}
	return nil
}
