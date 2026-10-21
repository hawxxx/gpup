package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"gpup/internal/bench"
)

func TestCLIValidatesConcurrencyBeforeNetwork(t *testing.T) {
	cmd := newRoot()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"bench", "http://127.0.0.1:1/v1", "--db", filepath.Join(t.TempDir(), "cli.db"), "--model", "x", "--concurrency", "0", "--duration", "1ms"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("invalid concurrency accepted")
	}
}
func TestCLIRegistersTargetsAndListsThem(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cli.db")
	cmd := newRoot()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"target", "add", "local", "--db", db, "--url", "http://127.0.0.1:8000/v1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	cmd = newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"target", "list", "--db", db})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("local")) {
		t.Fatalf("missing target %s", out.String())
	}
}

func TestParsesCIGatesAndRejectsUnknownRules(t *testing.T) {
	drop, ttft, err := parseGateRules([]string{"throughput<-5%", "ttft-p99>+10%"})
	if err != nil || drop == nil || *drop != 5 || ttft == nil || *ttft != 10 {
		t.Fatalf("gates: %v %v %v", drop, ttft, err)
	}
	for _, rule := range []string{"anything", "throughput<5%", "ttft-p99>-10%", "throughput<-NaN%"} {
		if _, _, err := parseGateRules([]string{rule}); err == nil {
			t.Errorf("accepted %s", rule)
		}
	}
}

func TestSameEndpointIgnoresVersionSuffix(t *testing.T) {
	if !sameEndpoint("http://127.0.0.1:8000/v1", "http://127.0.0.1:8000/") || sameEndpoint("http://127.0.0.1:8000", "http://127.0.0.1:8001") {
		t.Fatal("endpoint normalization mismatch")
	}
}

func TestRunCSVLeavesUnavailableReadingsEmpty(t *testing.T) {
	tps := 42.5
	run := bench.Run{ID: "r", Name: "base", Model: "m", Points: []bench.Point{{Concurrency: 2, Requests: 10, RPS: 1.5, OutputTokensPerSecond: &tps, E2E: bench.Percentiles{P50: 10, P95: 20, P99: 30}}}}
	var out bytes.Buffer
	if err := writeRunCSV(&out, run); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || lines[1] != "r,base,m,2,10,0,0.000,1.500,42.500,,,,,,,,10.000,20.000,30.000,false" {
		t.Fatalf("unexpected CSV:\n%s", out.String())
	}
}

func TestRunsCommandListsEmptyWorkspace(t *testing.T) {
	cmd := newRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"runs", "--db", filepath.Join(t.TempDir(), "cli.db")})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No saved runs") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}
