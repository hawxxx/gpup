# Metric semantics

GPUP measures request timing at the client. Numeric request timings use milliseconds; timestamps use RFC3339. Unknown measurements serialize as null or are omitted. Empty collections remain arrays. Measurements carry their collection source, and demo data is visibly labeled.

| Reading | Meaning and limits |
| --- | --- |
| TTFT | Elapsed time until first non-empty streamed content arrives, including `reasoning_content` or `reasoning` deltas from reasoning models. Includes transport and engine queueing. |
| E2E | Client elapsed time until stream completion. |
| TPOT | Time from first to last content arrival divided by known output token count minus one; unavailable without authoritative token usage or with fewer than two output tokens. This is a client chunk-derived average, not server token timing. |
| Chunk gaps / ITL | Gaps between content chunks; a chunk may contain multiple tokens. Benchmark points summarize them as `itl` percentiles, which describe chunk arrival rather than token-level latency. Per-request gap arrays are dropped after aggregation and are not kept in snapshots or stored request metadata. |
| Output throughput | Authoritative completed output tokens divided by measurement window seconds. Unknown usage does not become zero token usage. |
| Percentiles | Summaries of successful measured requests at an operating point. Errors are counted separately. |
| Load model | A closed-loop point runs N workers that each send the next request when the previous one finishes, so offered load adapts to latency. An open-loop point (`requestRates`) sends requests as a Poisson process at a fixed offered rate with a reproducible seed per rate, capped at N in flight; latency then includes queueing in the engine. Closed- and open-loop runs cannot be compared. |
| Delayed | Open-loop arrivals that found N requests already in flight and waited for a slot. A non-zero count means the offered rate was not delivered as scheduled. |
| Cut off (`incomplete`) | Measured requests still in flight when the point's window ended. They are cancelled and excluded from counts, percentiles, and throughput, which slightly favors short requests; the count shows how many. |
| Saturation (open loop) | The lowest offered rate with delayed arrivals or with successful completions more than three standard deviations below the expected Poisson count. Concurrency knees are reported only for closed-loop runs. |
| Goodput | With an SLO (`--slo-ttft`, `--slo-tpot`, `--slo-e2e`), the share of measured requests, errors included, that meet every target, and those requests per second. A TPOT target needs reported token usage; without it, goodput is unavailable rather than zero. A single-token response cannot miss a TPOT target. |
| Error classes | Failed requests are classified as `http_4xx`, `http_5xx`, `timeout`, `connection`, `stream_error` (an `error` object in the stream), `truncated` (no `[DONE]`), `malformed` (invalid JSON or oversized lines), or `other`. Points report counts per class. |
| Warm-up | Requests issued during a point's optional warm-up window appear in live views but are excluded from that point's counts, percentiles, and throughput window. Runs with different warm-up settings cannot be compared. |
| GPU utilization | NVML or nvidia-smi compute utilization percent, not a proof of inference efficiency. |
| Memory utilization | Memory controller busy percent; distinct from occupied VRAM (`memoryUsedMB / memoryTotalMB`). |
| Memory MB | GPU memory amounts represented in MiB despite legacy JSON field names ending in MB. |
| Power / clocks | Watts and MHz; device support and permissions vary. |
| PCIe rates | NVML throughput converted from KiB/s to MiB/s; unavailable in CLI fallback. |
| ECC | Volatile uncorrected error count where supported. Corrected/lifetime counters are not combined. |
| XID | DCGM's latest reported XID error code, not a count of errors. |
| Throttled | Active clock event (throttle) reasons excluding GPU idle, from NVML or nvidia-smi. Unavailable without a supported reading. |
| Topology | Best-effort active NVLink links and remote PCI addresses. Unsupported links remain absent. |
| NVLink rates | Unavailable; exporter cumulative byte counters are not substituted for throughput. |

Engine exporter metrics retain their source names. Supported vLLM, SGLang, llama.cpp, and TGI queue, running-request, KV-cache, and token counter series are normalized to shared keys when present. When a series has several label sets, such as data-parallel ranks or several served models, running and waiting requests and token counters are summed and KV-cache usage reports the highest rank. When two source names map to one key, such as a deprecated alias beside its replacement, the first series in the exposition is used so values are not double counted; missing queue, cache, or latency metrics remain unavailable. Exporter metrics and client timings are different measurement domains and should not be silently mixed. Names differ across engine releases: consult [vLLM production metrics](https://docs.vllm.ai/en/latest/usage/metrics/) for the engine version in use.

GPUP's own Prometheus endpoint (`/metrics`) exports target health, normalized engine readings (`gpup_target_requests_running`, `gpup_target_requests_waiting`, `gpup_target_kv_cache_usage_ratio`), and per-GPU utilization, memory, temperature, and power gauges.

Optional DCGM scraping is deadline- and response-size-bounded, matches device UUID, and supplements XID/ECC on already identified local devices. DCGM does not create remote GPUs in the snapshot. Metric codes and support depend on hardware and exporter configuration; see [NVIDIA DCGM Exporter metrics](https://docs.nvidia.com/datacenter/dcgm/latest/reference/dcgm-exporter-metrics.html).

Saturation findings use measured throughput and latency trends; observations do not establish causality. Hardware accuracy, sampling overhead, and topology correctness must be checked against a real deployment before relying on them for capacity decisions.
