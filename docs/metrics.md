# Metric semantics

GPUP measures request timing at the client. Numeric request timings use milliseconds; timestamps use RFC3339. Unknown measurements serialize as null or are omitted. Empty collections remain arrays. Measurements carry their collection source, and demo data is visibly labeled.

| Reading | Meaning and limits |
| --- | --- |
| TTFT | Elapsed time until first non-empty streamed content arrives. Includes transport and engine queueing. |
| E2E | Client elapsed time until stream completion. |
| TPOT | Time from first to last content arrival divided by known output token count minus one; unavailable without authoritative token usage or with fewer than two output tokens. This is a client chunk-derived average, not server token timing. |
| Chunk gaps | Gaps between content chunks; a chunk may contain multiple tokens. These are not token-level ITL. |
| Output throughput | Authoritative completed output tokens divided by measurement window seconds. Unknown usage does not become zero token usage. |
| Percentiles | Summaries of successful measured requests at a concurrency step. Errors are counted separately. |
| GPU utilization | NVML or nvidia-smi compute utilization percent, not a proof of inference efficiency. |
| Memory utilization | Memory controller busy percent; distinct from occupied VRAM (`memoryUsedMB / memoryTotalMB`). |
| Memory MB | GPU memory amounts represented in MiB despite legacy JSON field names ending in MB. |
| Power / clocks | Watts and MHz; device support and permissions vary. |
| PCIe rates | NVML throughput converted from KiB/s to MiB/s; unavailable in CLI fallback. |
| ECC | Volatile uncorrected error count where supported. Corrected/lifetime counters are not combined. |
| XID | DCGM's latest reported XID error code, not a count of errors. |
| Throttled | NVML active throttle reasons excluding GPU idle. Unavailable without a supported reading. |
| Topology | Best-effort active NVLink links and remote PCI addresses. Unsupported links remain absent. |
| NVLink rates | Unavailable; exporter cumulative byte counters are not substituted for throughput. |

Engine exporter metrics retain their source names. Supported vLLM fields are normalized when present; missing queue, cache, or latency metrics remain unavailable. Exporter metrics and client timings are different measurement domains and should not be silently mixed. Names differ across engine releases: consult [vLLM production metrics](https://docs.vllm.ai/en/latest/usage/metrics/) for the engine version in use.

Optional DCGM scraping is deadline- and response-size-bounded, matches device UUID, and supplements XID/ECC on already identified local devices. DCGM does not create remote GPUs in the snapshot. Metric codes and support depend on hardware and exporter configuration; see [NVIDIA DCGM Exporter metrics](https://docs.nvidia.com/datacenter/dcgm/latest/reference/dcgm-exporter-metrics.html).

Saturation findings use measured throughput and latency trends; observations do not establish causality. Hardware accuracy, sampling overhead, and topology correctness must be checked against a real deployment before relying on them for capacity decisions.
