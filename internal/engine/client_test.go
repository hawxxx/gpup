package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamUsageTail(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello world\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer s.Close()
	c, e := New(Target{ID: "t", Engine: "vllm", URL: s.URL})
	if e != nil {
		t.Fatal(e)
	}
	r, e := c.Generate(context.Background(), "m", "p", 8)
	if e != nil {
		t.Fatal(e)
	}
	if r.OutputTokens == nil || *r.OutputTokens != 2 || r.TTFTMs == nil || r.TPOTMs == nil {
		t.Fatalf("missing usage/timing: %+v", r)
	}
}

func TestCancelledStream(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	c, _ := New(Target{Engine: "tgi", URL: s.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := c.Generate(ctx, "m", "p", 8); e == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation too slow")
	}
}

func TestPreemptionMetric(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "vllm:num_preemptions_total{model_name=\"m\"} 4\n")
	}))
	defer s.Close()
	c, _ := New(Target{Engine: "vllm", URL: s.URL, MetricsURL: s.URL})
	m, e := c.Scrape(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if m["preemptionsTotal"].Value != 4 {
		t.Fatal("missing real preemptions metric")
	}
}
func TestStreamUnknownUsage(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"many words here\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer s.Close()
	c, _ := New(Target{Engine: "ollama", URL: s.URL})
	r, e := c.Generate(context.Background(), "m", "p", 8)
	if e != nil {
		t.Fatal(e)
	}
	if r.OutputTokens != nil || r.TPOTMs != nil {
		t.Fatal("invented tokens")
	}
}
func TestInvalidStreams(t *testing.T) {
	for _, body := range []string{"data: nope\n\n", "data: {\"choices\":[]}\n\n"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		c, _ := New(Target{Engine: "vllm", URL: s.URL})
		if _, e := c.Generate(context.Background(), "m", "p", 8); e == nil {
			t.Fatal("accepted invalid stream")
		}
		s.Close()
	}
}

func TestScrapeSpacedLabels(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "# HELP vllm:num_requests_waiting waiting\nvllm:num_requests_waiting{model_name=\"my model\"} 4 1700000000000\nvllm:kv_cache_usage_perc{model_name=\"my model\"} 0.5\n")
	}))
	defer s.Close()
	c, _ := New(Target{Engine: "vllm", URL: s.URL, MetricsURL: s.URL + "/metrics"})
	values, err := c.Scrape(context.Background())
	if err != nil || values["requestsWaiting"].Value != 4 || values["kvCacheFraction"].Value != 0.5 {
		t.Fatalf("values=%v err=%v", values, err)
	}
}

func TestHTTPErrorDetail(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":{"message":"The model `+"`missing`"+` does not exist."}}`)
	}))
	defer s.Close()
	c, _ := New(Target{Engine: "vllm", URL: s.URL})
	_, err := c.Generate(context.Background(), "missing", "p", 8)
	if err == nil || err.Error() != "engine HTTP 404: The model `missing` does not exist." {
		t.Fatalf("err=%v", err)
	}
}

func TestResultIDsUnique(t *testing.T) {
	c, _ := New(Target{Engine: "vllm", URL: "http://127.0.0.1:1"})
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		r, _ := c.Generate(context.Background(), "m", "p", 0)
		if seen[r.ID] {
			t.Fatalf("duplicate result ID %s", r.ID)
		}
		seen[r.ID] = true
	}
}

func TestReasoningTokensCountTowardTTFT(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer s.Close()
	c, _ := New(Target{Engine: "vllm", URL: s.URL})
	r, e := c.Generate(context.Background(), "m", "p", 8)
	if e != nil {
		t.Fatal(e)
	}
	if r.TTFTMs == nil || *r.TTFTMs >= 30 || len(r.ChunkGapsMs) != 1 || r.TPOTMs == nil {
		t.Fatalf("reasoning delta not treated as first token: %+v", r)
	}
}

func TestStreamErrorDetail(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"KV cache exhausted\"}}\n\n")
	}))
	defer s.Close()
	c, _ := New(Target{Engine: "vllm", URL: s.URL})
	_, e := c.Generate(context.Background(), "m", "p", 8)
	if e == nil || e.Error() != "engine returned stream error: KV cache exhausted" {
		t.Fatalf("stream error detail lost: %v", e)
	}
}

func TestScrapeEngineExporters(t *testing.T) {
	body := "sglang:num_running_reqs{model_name=\"m\"} 3\nsglang:num_queue_reqs{model_name=\"m\"} 1\nsglang:token_usage{model_name=\"m\"} 0.5\nllamacpp:requests_deferred 2\ntgi_queue_size 4\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer s.Close()
	c, _ := New(Target{Engine: "sglang", URL: s.URL, MetricsURL: s.URL + "/metrics"})
	m, e := c.Scrape(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if m["requestsRunning"].Value != 3 || m["kvCacheFraction"].Value != 0.5 || m["requestsWaiting"].Source != "sglang:num_queue_reqs" {
		t.Fatalf("SGLang series not normalized: %+v", m)
	}
}
