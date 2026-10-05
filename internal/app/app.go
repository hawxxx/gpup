package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"gpup/internal/analysis"
	"gpup/internal/bench"
	"gpup/internal/engine"
	"gpup/internal/gpu"
	"gpup/internal/storage"
)

const Version = "0.1.0"

type SnapshotPoint struct {
	Time            time.Time `json:"time"`
	TokensPerSecond *float64  `json:"tokensPerSecond,omitempty"`
	RPS             *float64  `json:"rps,omitempty"`
	TTFTMs          *float64  `json:"ttftMs,omitempty"`
	GPUUtilization  *float64  `json:"gpuUtilization,omitempty"`
	KVCachePercent  *float64  `json:"kvCachePercent,omitempty"`
}
type Snapshot struct {
	Time            time.Time       `json:"time"`
	Mode            string          `json:"mode"`
	Targets         []engine.Target `json:"targets"`
	Models          []engine.Model  `json:"models"`
	GPUs            []gpu.GPU       `json:"gpus"`
	Requests        []engine.Result `json:"requests"`
	Runs            []bench.Run     `json:"runs"`
	Findings        []bench.Finding `json:"findings"`
	History         []SnapshotPoint `json:"history"`
	CollectorErrors []string        `json:"collectorErrors"`
	Metrics         map[string]any  `json:"metrics"`
	System          map[string]any  `json:"system"`
}
type Job struct {
	ID     string     `json:"id"`
	Status string     `json:"status"`
	Error  string     `json:"error,omitempty"`
	Run    *bench.Run `json:"run,omitempty"`
}
type GPUSample struct {
	Time  time.Time `json:"time"`
	GPUs  []gpu.GPU `json:"gpus"`
	Scope string    `json:"scope"`
}
type requestBucket struct {
	Count        int
	Tokens       int
	UnknownUsage bool
	TTFT         []float64
}
type App struct {
	mu                  sync.RWMutex
	store               *storage.Store
	collector           *gpu.Collector
	state               Snapshot
	jobs                map[string]Job
	cancels             map[string]context.CancelFunc
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	closed              bool
	revision            uint64
	gpuSamples          []GPUSample
	completionBuckets   map[int64]*requestBucket
	loadedRequestChunks map[string]bool
}

func New(path, dcgmURL string) (*App, error) {
	s, err := storage.Open(path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{store: s, collector: gpu.NewCollector(dcgmURL), ctx: ctx, cancel: cancel, jobs: map[string]Job{}, cancels: map[string]context.CancelFunc{}, completionBuckets: map[int64]*requestBucket{}, loadedRequestChunks: map[string]bool{}}
	a.state = Snapshot{Time: time.Now(), Mode: "live", Targets: []engine.Target{}, Models: []engine.Model{}, GPUs: []gpu.GPU{}, Requests: []engine.Result{}, Runs: []bench.Run{}, Findings: []bench.Finding{}, History: []SnapshotPoint{}, CollectorErrors: []string{}, Metrics: map[string]any{}, System: map[string]any{"version": Version, "os": runtime.GOOS, "arch": runtime.GOARCH, "goVersion": runtime.Version(), "retention": "24h raw snapshots / 200 runs", "telemetryIntervalSeconds": 5, "promptPersistence": false, "requestScope": "GPUP-generated requests only", "capabilities": []string{"OpenAI-compatible streaming", "vLLM exporter", "NVML / nvidia-smi", "optional DCGM exporter", "SQLite WAL", "SSE"}}}
	for _, kind := range []string{"target", "run", "requests"} {
		limit := 200
		if kind == "requests" {
			limit = 2
		}
		rows, e := s.List(kind, limit)
		if e != nil {
			a.Close()
			return nil, e
		}
		for _, row := range rows {
			if kind == "target" {
				var t engine.Target
				if e = json.Unmarshal(row, &t); e == nil {
					a.state.Targets = append(a.state.Targets, t)
				}
			} else if kind == "run" {
				var r bench.Run
				if e = json.Unmarshal(row, &r); e == nil {
					a.state.Runs = append(a.state.Runs, summaryRun(r))
				}
			} else {
				var requests []engine.Result
				if e = json.Unmarshal(row, &requests); e == nil {
					a.state.Requests = append(a.state.Requests, requests...)
					if len(requests) > 0 {
						a.loadedRequestChunks[requestChunkKey(requests)] = true
					}
				}
			}
		}
	}
	sort.Slice(a.state.Requests, func(i, j int) bool { return a.state.Requests[i].StartedAt.Before(a.state.Requests[j].StartedAt) })
	if len(a.state.Requests) > 2000 {
		a.state.Requests = a.state.Requests[len(a.state.Requests)-2000:]
	}
	return a, nil
}
func ID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}
func (a *App) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.cancel()
	for _, cancel := range a.cancels {
		cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
	a.collector.Close()
	return a.store.Close()
}
func (a *App) Stop()                 { a.cancel() }
func (a *App) Done() <-chan struct{} { return a.ctx.Done() }
func (a *App) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	// The copy keeps HTTP encoders and CLI renderers outside the mutation lock.
	b, _ := json.Marshal(a.state)
	var s Snapshot
	_ = json.Unmarshal(b, &s)
	return s
}
func (a *App) Revision() uint64 { a.mu.RLock(); defer a.mu.RUnlock(); return a.revision }
func (a *App) AddTarget(ctx context.Context, t engine.Target) (engine.Target, error) {
	if t.Name == "" || len(t.Name) > 100 {
		return t, errors.New("target name required, maximum 100 characters")
	}
	if len(t.URL) > 2048 || len(t.Model) > 300 || len(t.APIKeyEnv) > 100 {
		return t, errors.New("target field too long")
	}
	if t.Engine == "" {
		t.Engine = "openai"
	}
	if t.ID == "" {
		t.ID = ID("target")
	}
	if _, err := engine.New(t); err != nil {
		return t, err
	}
	t.Status = "unknown"
	t.Error = ""
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return t, errors.New("app closed")
	}
	if len(a.state.Targets) >= 32 {
		return t, errors.New("maximum 32 targets")
	}
	for _, existing := range a.state.Targets {
		if existing.ID == t.ID || existing.Name == t.Name {
			return t, errors.New("target ID/name already exists")
		}
	}
	if err := a.store.Put("target", t.ID, t); err != nil {
		return t, err
	}
	a.state.Targets = append(a.state.Targets, t)
	a.revision++
	return t, nil
}
func (a *App) DeleteTarget(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.cancels) > 0 {
		return errors.New("cannot delete targets during a benchmark")
	}
	found := false
	out := []engine.Target{}
	for _, t := range a.state.Targets {
		if t.ID == id {
			found = true
		} else {
			out = append(out, t)
		}
	}
	if !found {
		return storage.ErrNotFound
	}
	if err := a.store.Delete("target", id); err != nil {
		return err
	}
	a.state.Targets = out
	models := []engine.Model{}
	for _, m := range a.state.Models {
		if m.TargetID != id {
			models = append(models, m)
		}
	}
	a.state.Models = models
	a.revision++
	return nil
}
func (a *App) Target(id string) (engine.Target, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, t := range a.state.Targets {
		if t.ID == id || t.Name == id {
			return t, nil
		}
	}
	return engine.Target{}, storage.ErrNotFound
}
func (a *App) Run(id string) (bench.Run, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, r := range a.state.Runs {
		if r.ID == id || (r.Name != "" && r.Name == id) {
			return r, nil
		}
	}
	return bench.Run{}, storage.ErrNotFound
}
func (a *App) Job(id string) (Job, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	j, ok := a.jobs[id]
	if ok && j.Status != "running" {
		var full bench.Run
		if err := a.store.Get("run", id, &full); err == nil {
			j.Run = &full
		}
		return j, nil
	}
	if !ok {
		for _, r := range a.state.Runs {
			if r.ID == id {
				var full bench.Run
				if err := a.store.Get("run", id, &full); err == nil {
					r = full
				}
				return Job{ID: id, Status: r.Status, Run: &r}, nil
			}
		}
		return Job{}, storage.ErrNotFound
	}
	return j, nil
}
func (a *App) CancelJob(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cancel, ok := a.cancels[id]
	if !ok {
		return storage.ErrNotFound
	}
	cancel()
	return nil
}

func (a *App) StartBenchmark(parent context.Context, c bench.Config) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	t, err := a.Target(c.TargetID)
	if err != nil {
		return "", errors.New("target not found")
	}
	c.TargetID = t.ID
	client, err := engine.New(t)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", errors.New("app closed")
	}
	if len(a.cancels) > 0 {
		return "", errors.New("a benchmark is already running")
	}
	id := ID("run")
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancels[id] = cancel
	a.jobs[id] = Job{ID: id, Status: "running"}
	safeConfig := c
	safeConfig.Prompt = ""
	a.state.Runs = append([]bench.Run{{ID: id, Name: c.Name, TargetID: t.ID, Model: c.Model, StartedAt: time.Now(), Status: "running", Config: safeConfig, Points: []bench.Point{}, Findings: []bench.Finding{}}}, a.state.Runs...)
	a.revision++
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		run, runErr := bench.RunBenchmark(ctx, client, c, a.recordRequest)
		run.ID = id
		run.Knee = analysis.DetectKnee(run.Points)
		a.mu.Lock()
		defer a.mu.Unlock()
		for i := range run.Points {
			run.Points[i].GPUSamples = []any{}
			for _, sample := range a.gpuSamples {
				if !sample.Time.Before(run.Points[i].StartedAt) && !sample.Time.After(run.Points[i].EndedAt) {
					run.Points[i].GPUSamples = append(run.Points[i].GPUSamples, sample)
				}
			}
		}
		// Host GPUs are not automatically attributable to this target, especially remote targets.
		// Keep device health in the host overview; run findings use workload evidence only.
		run.Findings = []bench.Finding{}
		if run.Knee != nil {
			run.Findings = append(run.Findings, bench.Finding{Kind: "saturation", Severity: "warning", Confidence: "medium", Summary: fmt.Sprintf("Observed concurrency knee near %d", *run.Knee), Evidence: []string{"Measured throughput flattened while E2E p95 latency increased; output tokens/sec is preferred when usage is known, otherwise requests/sec."}, Recommendation: "Rerun representative prompts around the knee before choosing a production limit."})
		}
		delete(a.cancels, id)
		summary := summaryRun(run)
		j := Job{ID: id, Status: run.Status, Run: &summary}
		if runErr != nil {
			j.Error = runErr.Error()
		}
		if err := a.store.Put("run", id, run); err != nil {
			j.Status = "error"
			j.Error = "persist run: " + err.Error()
		}
		requests := []engine.Result{}
		for _, request := range a.state.Requests {
			if request.TargetID == run.TargetID && !request.StartedAt.Before(run.StartedAt) {
				requests = append(requests, request)
			}
		}
		if err := a.store.Put("requests", id, requests); err != nil {
			j.Status = "error"
			j.Error = "persist request metadata: " + err.Error()
		}
		if len(requests) > 0 {
			a.loadedRequestChunks[requestChunkKey(requests)] = true
		}
		for i := range a.state.Runs {
			if a.state.Runs[i].ID == id {
				a.state.Runs[i] = summary
			}
		}
		if len(a.state.Runs) > 200 {
			a.state.Runs = a.state.Runs[:200]
		}
		a.jobs[id] = j
		a.revision++
		if len(a.jobs) > 200 {
			for key, job := range a.jobs {
				if key != id && job.Status != "running" {
					delete(a.jobs, key)
					break
				}
			}
		}
	}()
	return id, nil
}
func (a *App) recordRequest(r engine.Result) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recordRequestLocked(r)
}
func (a *App) recordRequestLocked(r engine.Result) {
	a.revision++
	a.state.Requests = append(a.state.Requests, r)
	if len(a.state.Requests) > 2000 {
		a.state.Requests = a.state.Requests[len(a.state.Requests)-2000:]
	}
	if r.Status == "ok" {
		second := r.StartedAt.Add(time.Duration(r.DurationMs * float64(time.Millisecond))).Unix()
		bucket := a.completionBuckets[second]
		if bucket == nil {
			bucket = &requestBucket{}
			a.completionBuckets[second] = bucket
		}
		bucket.Count++
		if r.OutputTokens != nil {
			bucket.Tokens += *r.OutputTokens
		} else {
			bucket.UnknownUsage = true
		}
		if r.TTFTMs != nil && len(bucket.TTFT) < 512 {
			bucket.TTFT = append(bucket.TTFT, *r.TTFTMs)
		}
		for old := range a.completionBuckets {
			if old < second-15 {
				delete(a.completionBuckets, old)
			}
		}
	}
}

func (a *App) Start() {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.Poll(a.ctx)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				a.Poll(a.ctx)
			}
		}
	}()
}
func (a *App) Poll(ctx context.Context) {
	if err := a.refreshStoredData(); err != nil {
		a.mu.Lock()
		a.state.CollectorErrors = []string{"storage refresh: " + err.Error()}
		a.mu.Unlock()
		return
	}
	snapshot := a.Snapshot()
	devices, gpuErr := a.collector.Collect(ctx)
	models := []engine.Model{}
	targets := snapshot.Targets
	errs := []string{}
	if gpuErr != nil {
		errs = append(errs, gpuErr.Error())
	}
	metrics := map[string]any{}
	var observation analysis.Observation
	for _, g := range devices {
		if g.Utilization != nil {
			observation.GPUUtilization = append(observation.GPUUtilization, *g.Utilization)
		}
		if g.MemoryUtilization != nil {
			observation.MemoryControllerPercent = append(observation.MemoryControllerPercent, *g.MemoryUtilization)
		}
		if g.TemperatureC != nil && g.Throttled != nil {
			observation.TemperatureC = append(observation.TemperatureC, *g.TemperatureC)
			observation.Throttled = append(observation.Throttled, *g.Throttled)
		}
	}
	for i, t := range targets {
		if ctx.Err() != nil {
			return
		}
		c, e := engine.New(t)
		if e != nil {
			targets[i].Status = "error"
			targets[i].Error = e.Error()
			continue
		}
		pollCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ms, e := c.Discover(pollCtx)
		cancel()
		if e != nil {
			targets[i].Status = "unreachable"
			targets[i].Error = e.Error()
		} else {
			targets[i].Status = "healthy"
			targets[i].Error = ""
			models = append(models, ms...)
		}
		if t.MetricsURL != "" {
			pollCtx, cancel = context.WithTimeout(ctx, 2*time.Second)
			values, e := c.Scrape(pollCtx)
			cancel()
			if e != nil {
				errs = append(errs, t.Name+": "+e.Error())
			} else {
				metrics[t.ID] = values
				for k, v := range values {
					val := v.Value
					switch k {
					case "kvCacheFraction":
						val *= 100
						observation.KVCachePercent = &val
					case "requestsWaiting":
						observation.RequestsWaiting = &val
					case "preemptionsTotal":
						observation.Evictions = &val
					}
				}
			}
		}
	}
	point := SnapshotPoint{Time: time.Now(), KVCachePercent: observation.KVCachePercent}
	if len(observation.GPUUtilization) > 0 {
		sum := 0.
		for _, v := range observation.GPUUtilization {
			sum += v
		}
		mean := sum / float64(len(observation.GPUUtilization))
		point.GPUUtilization = &mean
	}
	cutoff := point.Time.Add(-10 * time.Second).Unix()
	count, tokens, known := 0, 0, true
	ttft := []float64{}
	a.mu.Lock()
	for second, bucket := range a.completionBuckets {
		if second > cutoff {
			count += bucket.Count
			tokens += bucket.Tokens
			if bucket.UnknownUsage {
				known = false
			}
			ttft = append(ttft, bucket.TTFT...)
		} else if second < cutoff-5 {
			delete(a.completionBuckets, second)
		}
	}
	if count > 0 {
		rps := float64(count) / 10
		point.RPS = &rps
		if known {
			tps := float64(tokens) / 10
			point.TokensPerSecond = &tps
		}
		if len(ttft) > 0 {
			v := bench.Summarize(ttft).P50
			point.TTFTMs = &v
		}
	}
	// Merge health into the current target list; additions during collection cannot disappear.
	for i := range a.state.Targets {
		for _, t := range targets {
			if a.state.Targets[i].ID == t.ID {
				a.state.Targets[i].Status = t.Status
				a.state.Targets[i].Error = t.Error
			}
		}
	}
	a.state.Time = point.Time
	a.revision++
	a.state.GPUs = devices
	a.state.Models = models
	a.state.Metrics = metrics
	a.state.Findings = findingsForSnapshot(Snapshot{GPUs: devices, Metrics: metrics})
	a.state.CollectorErrors = errs
	a.gpuSamples = append(a.gpuSamples, GPUSample{Time: point.Time, GPUs: devices, Scope: "local host; target/device attribution unavailable"})
	if len(a.gpuSamples) > 7200 {
		a.gpuSamples = a.gpuSamples[len(a.gpuSamples)-7200:]
	}
	a.state.History = append(a.state.History, point)
	if len(a.state.History) > 600 {
		a.state.History = a.state.History[len(a.state.History)-600:]
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	hostname, _ := os.Hostname()
	a.state.System["hostname"] = hostname
	a.state.System["memoryMB"] = float64(memory.Alloc) / 1048576
	a.state.System["goroutines"] = runtime.NumGoroutine()
	a.mu.Unlock()
	_ = a.store.AddSnapshot(point.Time, map[string]any{"point": point, "gpus": devices, "metrics": metrics})
	_ = a.store.Prune(24*time.Hour, 200)
}

// Keep the shared SQLite workspace coherent across independent CLI and daemon processes.
func (a *App) refreshStoredData() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app closed")
	}
	rows, err := a.store.List("target", 32)
	if err != nil {
		return err
	}
	targets := []engine.Target{}
	for _, row := range rows {
		var t engine.Target
		if err = json.Unmarshal(row, &t); err != nil {
			return err
		}
		for _, current := range a.state.Targets {
			if current.ID == t.ID {
				t.Status = current.Status
				t.Error = current.Error
			}
		}
		targets = append(targets, t)
	}
	a.state.Targets = targets
	rows, err = a.store.List("run", 200)
	if err != nil {
		return err
	}
	runs := []bench.Run{}
	for _, run := range a.state.Runs {
		if _, active := a.cancels[run.ID]; active {
			runs = append(runs, run)
		}
	}
	for _, row := range rows {
		var run bench.Run
		if err = json.Unmarshal(row, &run); err != nil {
			return err
		}
		runs = append(runs, summaryRun(run))
	}
	if len(runs) > 200 {
		runs = runs[:200]
	}
	a.state.Runs = runs
	rows, err = a.store.List("requests", 2)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, request := range a.state.Requests {
		seen[request.ID] = true
	}
	for i := len(rows) - 1; i >= 0; i-- {
		var requests []engine.Result
		if err = json.Unmarshal(rows[i], &requests); err != nil {
			return err
		}
		key := requestChunkKey(requests)
		if a.loadedRequestChunks[key] {
			continue
		}
		a.loadedRequestChunks[key] = true
		for _, request := range requests {
			if !seen[request.ID] {
				a.recordRequestLocked(request)
				seen[request.ID] = true
			}
		}
	}
	if len(a.loadedRequestChunks) > 200 {
		current := map[string]bool{}
		for _, row := range rows {
			var requests []engine.Result
			_ = json.Unmarshal(row, &requests)
			current[requestChunkKey(requests)] = true
		}
		a.loadedRequestChunks = current
	}
	return nil
}
func requestChunkKey(requests []engine.Result) string {
	if len(requests) == 0 {
		return "empty"
	}
	return requests[0].ID + ":" + requests[len(requests)-1].ID
}
func summaryRun(run bench.Run) bench.Run {
	run.Points = append([]bench.Point{}, run.Points...)
	for i := range run.Points {
		run.Points[i].GPUSamples = nil
	}
	return run
}

func findingsForSnapshot(s Snapshot) []bench.Finding {
	var observation analysis.Observation
	for _, g := range s.GPUs {
		if g.Utilization != nil {
			observation.GPUUtilization = append(observation.GPUUtilization, *g.Utilization)
		}
		if g.MemoryUtilization != nil {
			observation.MemoryControllerPercent = append(observation.MemoryControllerPercent, *g.MemoryUtilization)
		}
		if g.TemperatureC != nil && g.Throttled != nil {
			observation.TemperatureC = append(observation.TemperatureC, *g.TemperatureC)
			observation.Throttled = append(observation.Throttled, *g.Throttled)
		}
	}
	findings := analysis.Findings(observation)
	for targetID, raw := range s.Metrics {
		values, ok := raw.(map[string]engine.Metric)
		if !ok {
			continue
		}
		targetObservation := analysis.Observation{}
		if v, ok := values["kvCacheFraction"]; ok {
			n := v.Value * 100
			targetObservation.KVCachePercent = &n
		}
		if v, ok := values["requestsWaiting"]; ok {
			n := v.Value
			targetObservation.RequestsWaiting = &n
		}
		for _, finding := range analysis.Findings(targetObservation) {
			finding.Evidence = append(finding.Evidence, "Inference target: "+targetID+"; hardware attribution unavailable.")
			findings = append(findings, finding)
		}
	}
	return findings
}

func (a *App) Discover(ctx context.Context) ([]engine.Target, error) {
	candidates := []struct {
		engine string
		port   int
	}{{"vllm", 8000}, {"sglang", 30000}, {"ollama", 11434}, {"llama.cpp", 8080}, {"triton", 8001}}
	found := []engine.Target{}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return found, ctx.Err()
		}
		endpoint := fmt.Sprintf("http://127.0.0.1:%d/v1", candidate.port)
		c, _ := engine.New(engine.Target{Engine: "openai", URL: endpoint})
		probe, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
		models, err := c.Discover(probe)
		cancel()
		if err != nil {
			continue
		}
		// Port defaults are hints, not proof of an engine identity.
		name := fmt.Sprintf("local-%d", candidate.port)
		if _, err := a.Target(name); err == nil {
			continue
		}
		t := engine.Target{Name: name, Engine: "openai", URL: endpoint}
		if len(models) > 0 {
			t.Model = models[0].ID
		}
		t, err = a.AddTarget(ctx, t)
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			return found, err
		}
		found = append(found, t)
	}
	return found, nil
}
