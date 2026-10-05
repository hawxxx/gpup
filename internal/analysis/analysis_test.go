package analysis

import (
	"gpup/internal/bench"
	"testing"
)

func TestEvidenceClassifications(t *testing.T) {
	zero := 0.0
	rate := 5.0
	gain := 3.0
	cases := []struct {
		o    Observation
		kind string
	}{{Observation{GPUUtilization: []float64{98}, MemoryControllerPercent: []float64{30}, RequestRate: &rate, ThroughputGainPercent: &gain}, "compute-bound"}, {Observation{GPUUtilization: []float64{60}, MemoryControllerPercent: []float64{95}, RequestRate: &rate, ThroughputGainPercent: &gain}, "memory-bandwidth-bound"}, {Observation{TemperatureC: []float64{92}, Throttled: []bool{true}}, "thermal-pressure"}, {Observation{GPUUtilization: []float64{8}, RequestRate: &rate, RequestsWaiting: &zero}, "underutilized"}}
	for _, c := range cases {
		found := false
		for _, f := range Findings(c.o) {
			if f.Kind == c.kind {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s", c.kind)
		}
	}
	for _, f := range Findings(Observation{GPUUtilization: []float64{99}}) {
		if f.Kind == "compute-bound" {
			t.Fatal("classified without scaling/controller evidence")
		}
	}
}
func TestTokensThresholdPreferred(t *testing.T) {
	drop := -20.0
	c := Comparison{Compatible: true, Changes: []Change{{RPSPercent: 2, OutputTokensPerSecondPercent: &drop}}}
	if CheckThresholds(c, 10, 10) == nil {
		t.Fatal("missed authoritative token throughput regression")
	}
}

func TestIncompatible(t *testing.T) {
	if _, e := Compare(bench.Run{Model: "a"}, bench.Run{Model: "b"}); e == nil {
		t.Fatal("accepted incompatible models")
	}
}

func TestRegressionThreshold(t *testing.T) {
	c := Comparison{Compatible: true, Changes: []Change{{Concurrency: 2, RPSPercent: -12, LatencyP95Percent: 5}}}
	if CheckThresholds(c, 10, 10) == nil {
		t.Fatal("missed throughput regression")
	}
	if CheckThresholds(c, 15, 10) != nil {
		t.Fatal("false regression")
	}
}
func TestKnee(t *testing.T) {
	p := []bench.Point{{Concurrency: 1, RPS: 10, E2E: bench.Percentiles{P95: 10}}, {Concurrency: 2, RPS: 10.5, E2E: bench.Percentiles{P95: 20}}}
	if k := DetectKnee(p); k == nil || *k != 1 {
		t.Fatal("missed saturation")
	}
}

func TestKneeUsesTokenThroughputWhenMeasured(t *testing.T) {
	a, b := 1000., 1020.
	points := []bench.Point{{Concurrency: 1, RPS: 10, OutputTokensPerSecond: &a, E2E: bench.Percentiles{P95: 10}}, {Concurrency: 2, RPS: 14, OutputTokensPerSecond: &b, E2E: bench.Percentiles{P95: 20}}}
	if k := DetectKnee(points); k == nil || *k != 1 {
		t.Fatal("missed token throughput saturation while request rate rises")
	}
}

func TestCompareRejectsFailedWorkloadPoints(t *testing.T) {
	baseline := bench.Run{Status: "complete", Model: "x", Points: []bench.Point{{Concurrency: 1, RPS: 10, E2E: bench.Percentiles{P95: 5}, Errors: 1}}}
	candidate := baseline
	candidate.Points = []bench.Point{{Concurrency: 1, RPS: 10, E2E: bench.Percentiles{P95: 5}}}
	if _, err := Compare(baseline, candidate); err == nil {
		t.Fatal("errorful baseline accepted")
	}
	if _, err := Compare(candidate, baseline); err == nil {
		t.Fatal("errorful candidate accepted")
	}
}

func TestCompareRejectsDuplicateConcurrencySets(t *testing.T) {
	p := bench.Point{Concurrency: 1, RPS: 10, E2E: bench.Percentiles{P95: 5}}
	a := bench.Run{Status: "complete", Points: []bench.Point{p, p}}
	q := p
	q.Concurrency = 2
	b := bench.Run{Status: "complete", Points: []bench.Point{p, q}}
	if _, err := Compare(a, b); err == nil {
		t.Fatal("duplicate concurrency comparison accepted")
	}
}
