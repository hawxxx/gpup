package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gpup/internal/bench"
	"gpup/internal/engine"
	"gpup/internal/gpu"
)

func TestTargetRunPersistenceAndPrivacy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"test-model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		f.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Millisecond):
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "gpup.db")
	a, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.AddTarget(context.Background(), engine.Target{Name: "fixture", Engine: "openai", URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.StartBenchmark(context.Background(), bench.Config{TargetID: target.ID, Model: "test-model", Prompt: "DO NOT STORE", Concurrency: []int{2}, DurationSeconds: .06, MaxTokens: 4})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		j, err := a.Job(id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != "running" {
			if j.Run == nil || j.Run.Config.Prompt != "" || len(j.Run.Points) != 1 {
				t.Fatalf("run privacy %+v", j)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.Close()
	a, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if len(a.Snapshot().Targets) != 1 || len(a.Snapshot().Runs) != 1 {
		t.Fatalf("not persisted: %+v", a.Snapshot())
	}
	if len(a.Snapshot().Requests) == 0 {
		t.Fatal("request metadata lost on restart")
	}
}

func TestRejectInvalidTargetAndUnknownBenchmark(t *testing.T) {
	a, err := New(filepath.Join(t.TempDir(), "gpup.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.AddTarget(context.Background(), engine.Target{Name: "bad", Engine: "openai", URL: "file:///etc/passwd"}); err == nil {
		t.Fatal("file target accepted")
	}
	if _, err = a.StartBenchmark(context.Background(), bench.Config{TargetID: "missing", Model: "x", Concurrency: []int{1}, DurationSeconds: 1, MaxTokens: 1}); err == nil {
		t.Fatal("missing target accepted")
	}
}

func TestLiveThroughputIndependentOfRequestExplorerRing(t *testing.T) {
	a, err := New(filepath.Join(t.TempDir(), "gpup.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tokens := 4
	for i := 0; i < 3000; i++ {
		a.recordRequest(engine.Result{ID: fmt.Sprint(i), StartedAt: time.Now(), Status: "ok", OutputTokens: &tokens})
	}
	a.Poll(context.Background())
	s := a.Snapshot()
	if len(s.Requests) != 2000 {
		t.Fatalf("explorer cap: %d", len(s.Requests))
	}
	p := s.History[len(s.History)-1]
	if p.RPS == nil || *p.RPS != 300 {
		t.Fatalf("throughput incorrectly capped: %+v", p.RPS)
	}
	if p.TokensPerSecond == nil || *p.TokensPerSecond != 1200 {
		t.Fatalf("tokens incorrectly capped: %+v", p.TokensPerSecond)
	}
}

func TestRunningJobAppearsInSnapshotAndCanCancel(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer fixture.Close()
	a, err := New(filepath.Join(t.TempDir(), "gpup.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	target, err := a.AddTarget(context.Background(), engine.Target{Name: "fixture", Engine: "openai", URL: fixture.URL})
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.StartBenchmark(context.Background(), bench.Config{TargetID: target.ID, Model: "x", Prompt: "private", Concurrency: []int{1}, DurationSeconds: 10, MaxTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := a.Snapshot()
	if len(s.Runs) != 1 || s.Runs[0].ID != id || s.Runs[0].Status != "running" || s.Runs[0].Config.Prompt != "" {
		t.Fatalf("active run missing or private: %+v", s.Runs)
	}
	if err = a.CancelJob(id); err != nil {
		t.Fatal(err)
	}
}

func TestHostFindingsDoNotAttributeRemoteTrafficToLocalGPUs(t *testing.T) {
	utilization := 5.0
	s := Snapshot{GPUs: []gpu.GPU{{Utilization: &utilization}}, History: []SnapshotPoint{{RPS: func() *float64 { v := 100.; return &v }()}}, Metrics: map[string]any{"remote": map[string]engine.Metric{"requestsWaiting": {Value: 0}, "kvCacheFraction": {Value: .95}}}}
	for _, f := range findingsForSnapshot(s) {
		if f.Kind == "underutilized" || f.Kind == "compute-bound" || f.Kind == "memory-bandwidth-bound" {
			t.Fatalf("remote traffic attributed to local GPU: %+v", f)
		}
	}
}

func TestRunningDaemonSeesOtherCLIProcessWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	daemon, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	cli, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	_, err = cli.AddTarget(context.Background(), engine.Target{Name: "from-cli", Engine: "openai", URL: "http://127.0.0.1:1/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = cli.store.Put("run", "external", bench.Run{ID: "external", Status: "complete", Points: []bench.Point{}}); err != nil {
		t.Fatal(err)
	}
	if err = cli.store.Put("requests", "external", []engine.Result{{ID: "external-request", Status: "ok", StartedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	daemon.Poll(context.Background())
	s := daemon.Snapshot()
	if len(s.Targets) != 1 || len(s.Runs) != 1 || len(s.Requests) != 1 {
		t.Fatalf("cached daemon missed CLI writes: targets=%d runs=%d requests=%d", len(s.Targets), len(s.Runs), len(s.Requests))
	}
}
