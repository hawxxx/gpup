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
