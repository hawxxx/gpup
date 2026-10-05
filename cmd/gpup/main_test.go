package main

import (
	"bytes"
	"path/filepath"
	"testing"
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
