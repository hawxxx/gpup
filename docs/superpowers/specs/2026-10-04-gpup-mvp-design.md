# GPUP MVP design

## Intent and acceptance

Build a local inference performance tool that connects streamed request timings, benchmark runs, and GPU observations. Preserve the GPUP name from the original request; InferScope is a reference specification. Deliver MVP 0.1 and useful deterministic analysis without claiming the later roadmap is implemented.

## Architecture

One Go executable embeds a React 19/TypeScript/Vite application. Cobra exposes serve/ui, target management, status, models, requests, gpu, top, bench, sweep, profile, compare, discover, and doctor. chi serves bounded JSON APIs and SSE. SQLite WAL stores targets, benchmark runs, request metadata, and bounded snapshot chunks. No prompt text or API keys enter SQLite.

Engine clients use HTTP streaming and explicit capabilities. OpenAI-compatible generation and model discovery support vLLM and compatible modes of SGLang, TensorRT-LLM, Triton, llama.cpp, Ollama, and TGI. Native non-OpenAI APIs are not inferred. vLLM metrics are normalized from its exporter with their source names. NVIDIA collection uses dynamically loaded NVML, with nvidia-smi fallback and optional DCGM exporter scraping. Unsupported fields are absent, never zero-filled measurements.

## Benchmark semantics

Closed-loop workers generate streamed chat completions with bounded concurrency and deadlines. TTFT measures first non-empty content arrival. Stream chunks are not tokens: output usage is authoritative; TPOT requires known output token count; ITL is unavailable unless token boundaries are known. Record chunk gaps separately. Aggregate successful requests, error counts, percentiles, completed output token throughput, and timeline GPU snapshots. Reject invalid durations/concurrency, malformed streams, and invalid endpoint URLs. Compare compatible workloads; CI gates fail on real regressions and invalid comparisons. Detect saturation only when throughput gains flatten and latency worsens.

## Interface

Dark-first compact dashboard with Overview, Models, Requests, GPUs, Benchmarks, Experiments, System. TanStack Router/Query/Table/Virtual, Radix, Tailwind, uPlot. Actual API data, clear empty/error states, opt-in labeled demonstration mode, bounded charts, keyboard-accessible target/benchmark dialogs. Live mode never fabricates measurements.

## Reliability and security

Loopback binding by default, optional bearer token, explicit token requirement for non-loopback binding, same-origin mutations, read-only mode, HTTP/body limits, cancellation, bounded benchmark concurrency, collector failure isolation, graceful shutdown. Credentials supplied through environment variables. Remote OIDC/RBAC, full OTLP, native non-OpenAI protocols, remote agents, NCCL, AMD/Intel, and automatic engine reconfiguration belong to later releases.

## Proof

Use local streaming fixtures for timings, usage, failures, cancellation, concurrency, comparison, retention, authentication, and persistence. Run Go tests/race/vet, TypeScript checks, frontend tests/build, binary smoke tests and browser verification. GPU validation requires available hardware; do not claim hardware accuracy or overhead targets without measurements.
