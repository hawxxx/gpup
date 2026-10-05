# Scope and roadmap

The MVP provides a local Go service/CLI, embedded React dashboard, SQLite persistence, OpenAI-compatible streamed requests, benchmark concurrency steps, run comparison, and deterministic findings. Live mode must show empty/error states when a source is unavailable. The legacy standalone `index.html` is simulated, and opt-in demo mode remains labeled.

Engine integration means OpenAI-compatible model discovery and chat completion on an explicitly configured URL. vLLM exporter metrics are supported when exposed. SGLang, TensorRT-LLM, Triton, llama.cpp, Ollama, and TGI are usable only when their configured endpoint implements the supported protocol; native engine APIs and universal exporter coverage are deferred.

NVIDIA collection uses dynamically loaded NVML in cgo builds, nvidia-smi fallback, and optional bounded DCGM supplements. Docker builds disable cgo, so native NVML is not available in the provided image. NVLink rates, token-level ITL, and unsupported GPU metrics remain unavailable. Basic collection was exercised on an RTX 5090; independent accuracy checks and workload overhead acceptance targets remain unverified.

Benchmarks vary client concurrency with bounded requests and deadlines. `sweep` does not tune batch sizes, scheduler policy, quantization, tensor parallelism, or engine deployment. `profile` records passive samples. Comparison requires compatible workloads and reports threshold failures. Prompt text and API keys are not persisted.

Docker, Compose, systemd, and Helm examples package a single local instance. Helm persistence is SQLite on a single replica. Distributed node collectors, cluster-wide aggregation, remote GPU discovery, multiuser RBAC/OIDC, automatic tuning, Nsight integration, AMD/Intel GPU support, and fleet orchestration are deferred. Packaging templates are starting points for operator verification, not a production certification.

Future increments should add native engine capability adapters with fixture tests, real GPU validation and sampling benchmarks, richer rate-based interconnect telemetry, then authenticated distributed collectors with explicit device identity and aggregation contracts. Each increment should update [metric semantics](metrics.md) and the [MVP design](superpowers/specs/2026-10-04-gpup-mvp-design.md) before introducing new claims.
