package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gpup/internal/promtext"
)

type Target struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Engine     string `json:"engine"`
	URL        string `json:"url"`
	Model      string `json:"model,omitempty"`
	APIKeyEnv  string `json:"apiKeyEnv,omitempty"`
	MetricsURL string `json:"metricsUrl,omitempty"`
	Status     string `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
}
type Model struct {
	ID       string `json:"id"`
	TargetID string `json:"targetId"`
	Engine   string `json:"engine"`
}
type Result struct {
	ID           string    `json:"id"`
	TargetID     string    `json:"targetId"`
	Model        string    `json:"model"`
	StartedAt    time.Time `json:"startedAt"`
	DurationMs   float64   `json:"durationMs"`
	TTFTMs       *float64  `json:"ttftMs,omitempty"`
	TPOTMs       *float64  `json:"tpotMs,omitempty"`
	OutputTokens *int      `json:"outputTokens,omitempty"`
	InputTokens  *int      `json:"inputTokens,omitempty"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	ChunkGapsMs  []float64 `json:"chunkGapsMs,omitempty"`
	Concurrency  int       `json:"concurrency,omitempty"`
}
type Metric struct {
	Value  float64 `json:"value"`
	Source string  `json:"source"`
}
type Capabilities struct {
	Protocol    string `json:"protocol"`
	Discovery   bool   `json:"discovery"`
	Streaming   bool   `json:"streaming"`
	Metrics     bool   `json:"metrics"`
	Description string `json:"description"`
}
type Client struct {
	Target       Target
	HTTP         *http.Client
	Capabilities Capabilities
}

// streamTransport is shared so per-poll clients reuse connections. It bounds the
// wait for response headers only: streamed generations may legitimately run
// longer than any fixed total timeout and are bounded by their context instead.
var streamTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 120 * time.Second
	return t
}()

// requestSequence keeps result IDs unique when concurrent requests start within
// the same clock tick, which is common on coarse-resolution clocks.
var requestSequence atomic.Uint64

// defaultTimeout bounds non-streaming calls whose caller set no deadline.
const defaultTimeout = 30 * time.Second

func New(t Target) (*Client, error) {
	if e := validURL(t.URL); e != nil {
		return nil, e
	}
	if t.MetricsURL != "" {
		if e := validURL(t.MetricsURL); e != nil {
			return nil, e
		}
	}
	switch strings.ToLower(t.Engine) {
	case "vllm", "sglang", "tensorrt-llm", "triton", "llama.cpp", "ollama", "tgi", "openai":
	default:
		return nil, fmt.Errorf("unsupported engine %q", t.Engine)
	}
	return &Client{Target: t, HTTP: &http.Client{Transport: streamTransport}, Capabilities: Capabilities{"OpenAI-compatible chat completions", true, true, t.MetricsURL != "", "Requires the engine's OpenAI-compatible endpoint; native APIs and token boundaries are unavailable."}}, nil
}
func validURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("endpoint must be an HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}
func (c *Client) endpoint(path string) string {
	base := strings.TrimRight(c.Target.URL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + path
	}
	return base + "/v1" + path
}
func (c *Client) request(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, method, endpoint, body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	if c.Target.APIKeyEnv != "" {
		key := os.Getenv(c.Target.APIKeyEnv)
		if key == "" {
			return nil, fmt.Errorf("credential environment variable %s is unset", c.Target.APIKeyEnv)
		}
		r.Header.Set("Authorization", "Bearer "+key)
	}
	resp, e := c.HTTP.Do(r)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		if detail := errorDetail(resp.Body); detail != "" {
			return nil, fmt.Errorf("engine HTTP %d: %s", resp.StatusCode, detail)
		}
		return nil, fmt.Errorf("engine HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// errorDetail extracts a short, single-line message from an engine error body.
func errorDetail(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, 4096))
	var payload struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
	}
	text := ""
	if json.Unmarshal(raw, &payload) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		var plain string
		switch {
		case json.Unmarshal(payload.Error, &nested) == nil && nested.Message != "":
			text = nested.Message
		case json.Unmarshal(payload.Error, &plain) == nil && plain != "":
			text = plain
		case payload.Message != "":
			text = payload.Message
		case payload.Detail != "":
			text = payload.Detail
		}
	} else if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<")) {
		text = string(raw)
	}
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200]) + "…"
	}
	return text
}
func withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, defaultTimeout)
}
func (c *Client) Discover(ctx context.Context) ([]Model, error) {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	resp, e := c.request(ctx, "GET", c.endpoint("/models"), nil)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); e != nil {
		return nil, e
	}
	out := []Model{}
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, Model{m.ID, c.Target.ID, c.Target.Engine})
		}
	}
	return out, nil
}
func (c *Client) Generate(ctx context.Context, model, prompt string, maxTokens int) (result Result, err error) {
	start := time.Now()
	result = Result{ID: strconv.FormatInt(start.UnixNano(), 36) + "-" + strconv.FormatUint(requestSequence.Add(1), 36), TargetID: c.Target.ID, Model: model, StartedAt: start, Status: "error"}
	defer func() {
		result.DurationMs = float64(time.Since(start)) / float64(time.Millisecond)
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Status = "ok"
		}
	}()
	if maxTokens < 1 {
		return result, errors.New("maxTokens must be positive")
	}
	body, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": prompt}}, "max_tokens": maxTokens, "stream": true, "stream_options": map[string]bool{"include_usage": true}})
	resp, e := c.request(ctx, "POST", c.endpoint("/chat/completions"), bytes.NewReader(body))
	if e != nil {
		return result, e
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 32<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	last := time.Time{}
	lastContentMs := 0.0
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					// Reasoning models stream thinking tokens separately; they are
					// generated output and must count toward TTFT and TPOT.
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				Prompt     *int `json:"prompt_tokens"`
				Completion *int `json:"completion_tokens"`
			} `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		if e := json.Unmarshal([]byte(data), &chunk); e != nil {
			return result, fmt.Errorf("malformed SSE JSON: %w", e)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			if detail := errorDetail(strings.NewReader(`{"error":` + string(chunk.Error) + `}`)); detail != "" {
				return result, fmt.Errorf("engine returned stream error: %s", detail)
			}
			return result, errors.New("engine returned stream error")
		}
		if chunk.Usage != nil {
			if chunk.Usage.Prompt != nil && *chunk.Usage.Prompt >= 0 {
				result.InputTokens = chunk.Usage.Prompt
			}
			if chunk.Usage.Completion != nil && *chunk.Usage.Completion >= 0 {
				result.OutputTokens = chunk.Usage.Completion
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" && choice.Delta.ReasoningContent == "" && choice.Delta.Reasoning == "" {
				continue
			}
			now := time.Now()
			ms := float64(now.Sub(start)) / float64(time.Millisecond)
			if result.TTFTMs == nil {
				v := ms
				result.TTFTMs = &v
			}
			if !last.IsZero() && len(result.ChunkGapsMs) < 10000 {
				result.ChunkGapsMs = append(result.ChunkGapsMs, float64(now.Sub(last))/float64(time.Millisecond))
			}
			last = now
			lastContentMs = ms
		}
	}
	if e := scanner.Err(); e != nil {
		return result, e
	}
	if !done {
		return result, errors.New("truncated SSE stream: missing [DONE]")
	}
	if result.OutputTokens != nil && *result.OutputTokens > 1 && result.TTFTMs != nil {
		v := (lastContentMs - *result.TTFTMs) / float64(*result.OutputTokens-1)
		result.TPOTMs = &v
	}
	return result, nil
}

// exporterNames maps engine exporter series to normalized metric keys. Engines
// expose the same concepts under different names; unknown series are ignored.
var exporterNames = map[string]string{
	"vllm:num_requests_running": "requestsRunning", "vllm:num_requests_waiting": "requestsWaiting",
	"vllm:gpu_cache_usage_perc": "kvCacheFraction", "vllm:kv_cache_usage_perc": "kvCacheFraction",
	"vllm:prompt_tokens_total": "inputTokensTotal", "vllm:generation_tokens_total": "outputTokensTotal",
	"vllm:num_preemptions_total": "preemptionsTotal",
	"sglang:num_running_reqs":    "requestsRunning", "sglang:num_queue_reqs": "requestsWaiting",
	"sglang:token_usage": "kvCacheFraction", "sglang:prompt_tokens_total": "inputTokensTotal",
	"sglang:generation_tokens_total": "outputTokensTotal",
	"llamacpp:requests_processing":   "requestsRunning", "llamacpp:requests_deferred": "requestsWaiting",
	"llamacpp:prompt_tokens_total": "inputTokensTotal", "llamacpp:tokens_predicted_total": "outputTokensTotal",
	"tgi_batch_current_size": "requestsRunning", "tgi_queue_size": "requestsWaiting",
}

func (c *Client) Scrape(ctx context.Context) (map[string]Metric, error) {
	out := map[string]Metric{}
	if c.Target.MetricsURL == "" {
		return out, errors.New("metrics endpoint not configured")
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	resp, e := c.request(ctx, "GET", c.Target.MetricsURL, nil)
	if e != nil {
		return out, e
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		sample, ok := promtext.ParseLine(scanner.Text())
		if !ok {
			continue
		}
		normalized, ok := exporterNames[sample.Name]
		if !ok {
			continue
		}
		key := normalized
		if _, exists := out[key]; exists {
			key = normalized + "/" + sample.Name + sample.Labels
		}
		out[key] = Metric{sample.Value, sample.Name}
	}
	return out, scanner.Err()
}
