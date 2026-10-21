package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestCredentialNotSentToForeignMetricsOrigin(t *testing.T) {
	t.Setenv("GPUP_TEST_KEY", "secret")
	seen := make(chan string, 2)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		fmt.Fprint(w, "vllm:num_requests_running 1\n")
	})
	engineServer, exporter := httptest.NewServer(handler), httptest.NewServer(handler)
	defer engineServer.Close()
	defer exporter.Close()
	for _, tc := range []struct{ metricsURL, want string }{
		{engineServer.URL + "/metrics", "Bearer secret"},
		{exporter.URL + "/metrics", ""},
	} {
		c, _ := New(Target{Engine: "vllm", URL: engineServer.URL + "/v1", APIKeyEnv: "GPUP_TEST_KEY", MetricsURL: tc.metricsURL})
		if _, e := c.Scrape(context.Background()); e != nil {
			t.Fatal(e)
		}
		if got := <-seen; got != tc.want {
			t.Fatalf("%s received Authorization %q, want %q", tc.metricsURL, got, tc.want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"http://h:8000/metrics", "http://h:8000/v1", true},
		{"http://H/metrics", "http://h:80/v1", true},
		{"https://h/metrics", "https://h:443/v1", true},
		{"http://h:9400/metrics", "http://h:8000/v1", false},
		{"https://h:8000/metrics", "http://h:8000/v1", false},
		{"http://evil/metrics", "http://h/v1", false},
	} {
		if got := sameOrigin(tc.a, tc.b); got != tc.want {
			t.Errorf("sameOrigin(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestScrapeAggregatesRanksAndIgnoresAliases(t *testing.T) {
	body := `vllm:num_requests_running{engine="0"} 3
vllm:num_requests_running{engine="1"} 2
vllm:kv_cache_usage_perc{engine="0"} 0.4
vllm:kv_cache_usage_perc{engine="1"} 0.9
vllm:gpu_cache_usage_perc{engine="0"} 0.1
vllm:generation_tokens_total{engine="0"} 100
vllm:generation_tokens_total{engine="1"} 50
`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer s.Close()
	c, _ := New(Target{Engine: "vllm", URL: s.URL, MetricsURL: s.URL + "/metrics"})
	m, e := c.Scrape(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if m["requestsRunning"].Value != 5 || m["outputTokensTotal"].Value != 150 {
		t.Fatalf("additive series not summed across ranks: %+v", m)
	}
	if m["kvCacheFraction"].Value != 0.9 || m["kvCacheFraction"].Source != "vllm:kv_cache_usage_perc" {
		t.Fatalf("KV cache must report the most pressured rank of the first series: %+v", m)
	}
	if len(m) != 3 {
		t.Fatalf("unexpected keys: %+v", m)
	}
}

func TestGenerateClassifiesErrors(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		handler    http.HandlerFunc
	}{
		{"rate limited", ErrorKindHTTP4xx, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }},
		{"server error", ErrorKindHTTP5xx, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }},
		{"truncated", ErrorKindTruncated, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		}},
		{"malformed", ErrorKindMalformed, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "data: {nope\n\n") }},
		{"engine", ErrorKindStream, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "data: {\"error\":{\"message\":\"oom\"}}\n\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(tc.handler)
			defer s.Close()
			c, _ := New(Target{Engine: "vllm", URL: s.URL})
			r, e := c.Generate(context.Background(), "m", "p", 8)
			if e == nil || r.ErrorKind != tc.kind || r.Status != "error" {
				t.Fatalf("got kind %q status %q err %v, want %q", r.ErrorKind, r.Status, e, tc.kind)
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := s.URL
	s.Close()
	c, _ := New(Target{Engine: "vllm", URL: url})
	if r, _ := c.Generate(context.Background(), "m", "p", 8); r.ErrorKind != ErrorKindConnection {
		t.Fatalf("closed server classified as %q: %s", r.ErrorKind, r.Error)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if ClassifyError(ctx.Err()) != ErrorKindTimeout {
		t.Fatal("deadline not classified as timeout")
	}
}

// bodyTransport serves a fixed SSE body without a network listener.
type bodyTransport string

func (b bodyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
}

func FuzzGenerateStream(f *testing.F) {
	for _, seed := range []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n",
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"t\"}}]}\ndata: [DONE]\n",
		"data: {\"error\":\"boom\"}\n\n",
		"data: {\"usage\":{\"completion_tokens\":-4}}\n\ndata: [DONE]\n\n",
		": comment\n\nevent: x\ndata:[DONE]\n",
		"data: {\"choices\":null}\n\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		c, _ := New(Target{Engine: "vllm", URL: "http://fuzz.invalid"})
		c.HTTP = &http.Client{Transport: bodyTransport(body)}
		r, e := c.Generate(context.Background(), "m", "p", 8)
		if (e == nil) != (r.Status == "ok") || (e == nil) != (r.ErrorKind == "") {
			t.Fatalf("status %q kind %q inconsistent with error %v", r.Status, r.ErrorKind, e)
		}
		if r.OutputTokens != nil && *r.OutputTokens < 0 || r.TTFTMs != nil && *r.TTFTMs < 0 || r.DurationMs < 0 {
			t.Fatalf("negative measurement: %+v", r)
		}
		if r.TPOTMs != nil && (r.OutputTokens == nil || *r.OutputTokens < 2) {
			t.Fatalf("TPOT without at least two reported tokens: %+v", r)
		}
	})
}
