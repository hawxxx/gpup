// This synthetic endpoint is only for integration tests and documentation
// screenshots. It is not an inference engine and its measurements are not real.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:17332", "Listen address")
	delay := flag.Duration("delay", 10*time.Millisecond, "Delay between synthetic content chunks")
	tokens := flag.Int("tokens", 4, "Synthetic content chunks per response")
	capacity := flag.Int("capacity", 0, "Concurrent requests served at full speed; 0 disables contention")
	flag.Parse()
	var running, waiting atomic.Int64
	var generated atomic.Int64
	var slots chan struct{}
	if *capacity > 0 {
		slots = make(chan struct{}, *capacity)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpup-test-fixture"}]}`)
	})
	// A vLLM-shaped exporter lets the dashboard exercise engine readings.
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		usage := 0.0
		if *capacity > 0 {
			usage = float64(running.Load()) / float64(*capacity)
		}
		fmt.Fprintf(w, "vllm:num_requests_running{model_name=\"gpup-test-fixture\"} %d\nvllm:num_requests_waiting{model_name=\"gpup-test-fixture\"} %d\nvllm:kv_cache_usage_perc{model_name=\"gpup-test-fixture\"} %g\nvllm:generation_tokens_total{model_name=\"gpup-test-fixture\"} %d\n", running.Load(), waiting.Load(), usage, generated.Load())
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if slots != nil {
			waiting.Add(1)
			select {
			case slots <- struct{}{}:
				waiting.Add(-1)
				defer func() { <-slots }()
			case <-r.Context().Done():
				waiting.Add(-1)
				return
			}
		}
		running.Add(1)
		defer running.Add(-1)
		w.Header().Set("Content-Type", "text/event-stream")
		count := *tokens
		for i := 0; i < count; i++ {
			// Decode slows as the synthetic batch fills, like a shared accelerator.
			step := *delay
			if *capacity > 0 {
				step = time.Duration(float64(step) * (1 + float64(running.Load())/float64(*capacity)))
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(step):
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"test \"}}]}\n\n")
			w.(http.Flusher).Flush()
			generated.Add(1)
		}
		fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":%d}}\n\ndata: [DONE]\n\n", count)
	})
	if err := http.ListenAndServe(*listen, mux); err != nil {
		panic(err)
	}
}
