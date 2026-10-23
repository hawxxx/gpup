# Robustness and feature gaps

This review compares GPUP with common inference benchmarking and observability tools: vLLM `benchmark_serving`, NVIDIA GenAI-Perf, LLMPerf, GuideLLM, and DCGM-based Grafana dashboards. It records the bugs fixed during the October 21, 2026 pass and ranks the remaining gaps.

## Fixed in this pass

| Area | Problem | Resolution |
| --- | --- | --- |
| Streaming timing | Reasoning models stream `reasoning_content` or `reasoning` deltas before visible content. TTFT counted only visible content, so it included the whole thinking phase; TPOT was skewed. | Reasoning deltas count as generated output for TTFT, chunk gaps, and TPOT. |
| Error reporting | In-stream `error` objects were reduced to `engine returned stream error`. | The engine message is preserved, such as `KV cache exhausted`. |
| Collection | Targets were probed one at a time with 2-second timeouts. With several unreachable targets, one poll could exceed the 5-second interval and delay telemetry. | Up to 8 targets are probed concurrently; results merge in configuration order. |
| Engine coverage | Only vLLM exporter series were recognized. | SGLang, llama.cpp, and TGI queue, running, KV-cache, and token counter series are normalized to the same keys. |
| Restart | Snapshot history was persisted but never reloaded, so charts restarted empty despite 24-hour retention. | The last 600 history points are restored at startup. |
| Discovery | Discovery probed Triton's gRPC port (8001), which cannot serve HTTP, and never configured metrics URLs. | Discovery probes Triton's OpenAI frontend (9000) and LM Studio (1234). It registers `/metrics` only when that endpoint exposes a recognized engine series. |
| Prometheus export | `/metrics` omitted engine queue and KV-cache readings. | It now exports `gpup_target_requests_running`, `gpup_target_requests_waiting`, and `gpup_target_kv_cache_usage_ratio`. |
| Run lifecycle | Saved runs could not be listed from the CLI, exported, or deleted. | Added `gpup runs`, `gpup runs export --format json\|csv`, `gpup runs delete`, `DELETE /api/v1/runs/{id}`, and dashboard export/delete actions. |
| Dashboard | The Overview request table showed the 20 oldest live requests instead of the newest. Request sorting compared timestamp strings, which can misorder fractional seconds. | Overview shows the newest 20. The request trace sorts newest first by parsed time. |
| Dashboard | KV-cache history and engine readings were collected but never displayed. | Overview shows a KV-cache chart when available; model cards show running, waiting, and KV-cache readings. |
| Dashboard | Benchmark charts were empty when servers omitted token usage. | The chart falls back to requests/s and labels the metric. The table adds TTFT p99, TPOT p50, and E2E p50; the knee row is highlighted. |
| Dashboard | Theme reset on every load, ignored the system preference, and chart grids stayed dark in light mode. Version labels were hard-coded. | Theme follows saved choice, then system preference; chart axes use theme tokens; version comes from the server. |
| Benchmarks | Cold-start requests (connection setup, CUDA graph capture, empty prefix cache) were measured. | Added an optional per-point warm-up (`--warmup`, dashboard field). Warm-up requests appear live but are excluded from statistics, and comparisons require matching warm-up. |
| Benchmarks | Chunk gaps were collected per request but discarded. | Points report `itl` percentiles (chunk arrival), shown as ITL p99 and exported in CSV. |
| GPU collection | The nvidia-smi fallback never reported throttling, so the thermal finding could not trigger in the Docker image. | Clock event reasons are queried separately, with the pre-R535 field name as fallback, so unsupported drivers keep base readings. |
| Dashboard | A running benchmark showed "0 operating points" with no progress. | Snapshots include active job progress; the dashboard shows point, concurrency, request, and error counts. |
| Dashboard | The benchmark dialog required typing the model even when the target had one. | Selecting a target prefills its default or discovered model. |

## Second pass (October 21, 2026)

| Area | Problem | Resolution |
| --- | --- | --- |
| Load model | Only closed-loop concurrency sweeps existed, which hide queueing delay. | `--request-rate` runs open-loop Poisson arrivals with an in-flight cap, reports delayed arrivals, and flags the first unsustained rate. |
| Cool-down | Requests in flight at a point's deadline were silently discarded. | They are still excluded from statistics but counted as `incomplete` ("cut off"). |
| Goodput | No SLO reporting. | `--slo-ttft`, `--slo-tpot`, `--slo-e2e` report goodput per point; `--fail-if 'goodput<95%'` gates CI. |
| Failure taxonomy | Errors were free-form strings. | Requests carry an `errorKind`; points count failures per class; the dashboard and CLI show the breakdown. |
| Duplicate run names | Saving over a name was silent. | The CLI warns that name lookups now resolve to the new run. |
| Exporter accuracy | With several label sets (data-parallel ranks, several models), only the first series counted. | Additive series are summed; KV-cache usage reports the highest rank; deprecated aliases are not double counted. |
| Credentials | The target's API key was sent to metrics URLs on any host. | Sent only to the target's own origin. |
| SSRF | Targets could reach link-local metadata services. | Link-local and unspecified addresses are refused after DNS resolution. |
| Session cookie | The cookie held the raw API token. | It holds an HMAC derived from the token. |
| Snapshot size | Every live snapshot carried each request's chunk-gap array; 2,000 requests produced 2.9 MB per update. | Gaps are dropped after aggregation: 0.59 MB, and `/api/v1/snapshot` latency fell from 61 ms to 8–17 ms on the same workspace. |
| Accessibility | Secondary text and the light-theme accent failed WCAG AA contrast; scrollable tables were not keyboard reachable; 320 px layouts overflowed. | axe-core WCAG 2.2 AA reports no violations on any page in either theme; no page overflows from 320 to 1920 px. |

## Remaining gaps, ranked

1. ~~**Open-loop load (request rate).**~~ Done in the second pass. GPUP only runs closed-loop concurrency sweeps. GuideLLM, GenAI-Perf, and vLLM's benchmark tool also offer fixed or Poisson request rates. Closed loops hide queueing delay because clients wait for responses. Add `--request-rate` with Poisson arrivals and report achieved versus offered rate.
2. ~~**Cool-down handling.**~~ Cut-off requests are now counted. Requests still in flight at a point's deadline are discarded, which slightly favors short requests at high concurrency. Consider reporting their count.
3. **Prompt datasets and length distributions.** One fixed prompt does not exercise prefix caching, batching, or context length realistically. Support JSONL prompt files, synthetic input/output length distributions, and `ignore_eos`-style fixed output lengths where the engine supports them. Hash the dataset, not the prompt text, for compatibility checks.
4. ~~**Goodput and SLO reporting.**~~ Done in the second pass. Report the share of requests meeting TTFT and TPOT targets, as in DistServe goodput, plus a `--fail-if 'goodput<95%'` gate.
5. **Token-level ITL.** Chunk-gap percentiles are now reported, but engines that batch tokens per chunk hide true inter-token latency. Engine-side histograms (for example vLLM's) could supply it where exposed.
6. ~~**Retry and failure taxonomy.**~~ Error classes are done; retries are intentionally not performed. Classify errors by HTTP status, timeout, truncated stream, or engine error. Show counts per class in each point and the dashboard.
7. **Request detail drawer.** Errors are only visible as row tooltips. Add a detail drawer with the error, token counts, chunk-gap sparkline, and concurrency.
8. **Alerting and notifications.** Findings are visible only in the UI. Expose them as Prometheus alerts or a webhook.
9. **Duplicate run names.** The CLI now warns. Names are not unique; lookups resolve to the newest run. A `--replace` flag or uniqueness warning would make CI baselines less ambiguous.
10. **Persistent request history.** Only the latest two request chunks are reloaded; long-term request analysis requires a request table with indexed time queries.
11. **Multi-instance aggregation.** Out of MVP scope, but the most requested capability for fleet operators.

## Robustness checklist for future increments

- Every new metric should state its source, unit, and unavailability behavior in [metric semantics](../metrics.md).
- Collectors should be time-bounded and isolated so one slow source cannot delay others.
- Benchmarks should record any setting that changes results in the workload hash.
- UI panels should hide or label missing data instead of showing zeros.
