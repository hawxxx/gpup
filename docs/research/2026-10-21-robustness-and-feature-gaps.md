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

## Remaining gaps, ranked

1. **Open-loop load (request rate).** GPUP only runs closed-loop concurrency sweeps. GuideLLM, GenAI-Perf, and vLLM's benchmark tool also offer fixed or Poisson request rates. Closed loops hide queueing delay because clients wait for responses. Add `--request-rate` with Poisson arrivals and report achieved versus offered rate.
2. **Cool-down handling.** Requests still in flight at a point's deadline are discarded, which slightly favors short requests at high concurrency. Consider reporting their count.
3. **Prompt datasets and length distributions.** One fixed prompt does not exercise prefix caching, batching, or context length realistically. Support JSONL prompt files, synthetic input/output length distributions, and `ignore_eos`-style fixed output lengths where the engine supports them. Hash the dataset, not the prompt text, for compatibility checks.
4. **Goodput and SLO reporting.** Report the share of requests meeting TTFT and TPOT targets, as in DistServe goodput, plus a `--fail-if 'goodput<95%'` gate.
5. **Token-level ITL.** Chunk-gap percentiles are now reported, but engines that batch tokens per chunk hide true inter-token latency. Engine-side histograms (for example vLLM's) could supply it where exposed.
6. **Retry and failure taxonomy.** Classify errors by HTTP status, timeout, truncated stream, or engine error. Show counts per class in each point and the dashboard.
7. **Request detail drawer.** Errors are only visible as row tooltips. Add a detail drawer with the error, token counts, chunk-gap sparkline, and concurrency.
8. **Alerting and notifications.** Findings are visible only in the UI. Expose them as Prometheus alerts or a webhook.
9. **Duplicate run names.** Names are not unique; lookups resolve to the newest run. A `--replace` flag or uniqueness warning would make CI baselines less ambiguous.
10. **Persistent request history.** Only the latest two request chunks are reloaded; long-term request analysis requires a request table with indexed time queries.
11. **Multi-instance aggregation.** Out of MVP scope, but the most requested capability for fleet operators.

## Robustness checklist for future increments

- Every new metric should state its source, unit, and unavailability behavior in [metric semantics](../metrics.md).
- Collectors should be time-bounded and isolated so one slow source cannot delay others.
- Benchmarks should record any setting that changes results in the workload hash.
- UI panels should hide or label missing data instead of showing zeros.
