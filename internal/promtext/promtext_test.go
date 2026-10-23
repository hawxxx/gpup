package promtext

import (
	"math"
	"testing"
)

func TestParseLineWithSpacedLabels(t *testing.T) {
	s, ok := ParseLine(`DCGM_FI_DEV_XID_ERRORS{gpu="0",UUID="GPU-1",modelName="NVIDIA GeForce RTX 4090",note="a \"}\" b"} 3 1700000000000`)
	if !ok || s.Name != "DCGM_FI_DEV_XID_ERRORS" || s.Value != 3 {
		t.Fatalf("parse failed: %+v %v", s, ok)
	}
	if v, ok := s.Label("UUID"); !ok || v != "GPU-1" {
		t.Fatalf("UUID label = %q %v", v, ok)
	}
	if v, ok := s.Label("modelName"); !ok || v != "NVIDIA GeForce RTX 4090" {
		t.Fatalf("modelName label = %q %v", v, ok)
	}
	if v, ok := s.Label("note"); !ok || v != `a "}" b` {
		t.Fatalf("escaped label = %q %v", v, ok)
	}
	if _, ok := s.Label("missing"); ok {
		t.Fatal("missing label reported present")
	}
}

func TestParseLineRejectsInvalid(t *testing.T) {
	for _, line := range []string{"", "# TYPE x gauge", "x", "x{a=\"1\"", "x NaN", "x +Inf", "x 1 2 3", "{a=\"1\"} 1"} {
		if _, ok := ParseLine(line); ok {
			t.Fatalf("accepted %q", line)
		}
	}
	if s, ok := ParseLine("vllm:num_requests_running 2"); !ok || s.Value != 2 || s.Labels != "" {
		t.Fatalf("unlabelled sample: %+v %v", s, ok)
	}
}

func FuzzParseLine(f *testing.F) {
	for _, seed := range []string{`a{b="c d}"} 1 2`, `x 1e3`, `# HELP x`, `y{z="\"}"} NaN`, `m{`, "n\t+Inf"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		s, ok := ParseLine(line)
		if ok && (s.Name == "" || math.IsNaN(s.Value) || math.IsInf(s.Value, 0)) {
			t.Fatalf("accepted invalid sample %+v from %q", s, line)
		}
	})
}
