# MVP verification

## Delivered artifact

`bin/gpup` is a Linux amd64 Go executable with embedded production frontend assets. The stripped native build is about 14 MiB. Frontend JavaScript totals 184,422 bytes gzipped; Node is needed for building, not running the binary. `AGENTS.md` was preserved.

## Automated proof

- 44 Go test functions cover stream timing, missing usage, malformed/truncated responses, cancellations, workload bounds, workers, reservoir percentiles, token-throughput saturation, comparisons, deterministic findings, GPU parsing/fallback, SQLite retention, shared-process refresh, privacy, and API security/lifecycle.
- `go test -race -timeout 60s ./...`: passed.
- `go vet ./...`: passed.
- `CGO_ENABLED=0 go test -timeout 60s ./...`: passed.
- `npm --prefix web test`: eight tests passed.
- `npm --prefix web run build`: TypeScript and Vite passed.
- Docker image build and runtime smoke: passed. Unauthenticated API returned 401; authenticated snapshot returned 200. Missing GPU utilities produced collector diagnostics rather than a crash.
- Compose configuration validation and Helm 3.18.6 lint/template rendering: passed. Helm reported only its optional icon recommendation.

## Product loop exercised

The synthetic HTTP endpoint in `benchmarks/fixture` produced streamed responses with explicit usage. A concurrency 1/2 sweep saved real client timings and token counts. Reopening SQLite retained request metadata and runs. Comparing a run to itself produced zero deltas. A deliberately slower fixture reduced output throughput by approximately 65–67%; regression gates exited 1. These numbers verify the tool, not an inference engine's performance.

Live UI dialogs registered a target, launched a two-point run, displayed completion, and canceled a longer run through the API. All navigation views were checked at mobile width without overflow or JavaScript errors. The TUI displayed native GPU telemetry, switched views, and quit cleanly.

## Hardware and scope limits

NVML reported an NVIDIA GeForce RTX 5090 with utilization, memory, temperature, power, clocks, and PCIe measurements. Independent accuracy checks, sustained benchmark overhead, GPU topology correctness, and the stated latency/idle CPU targets remain unverified. One short runtime observation measured roughly 69.5 MiB RSS and 1% CPU over two seconds; it is not an idle-performance acceptance test. Idle SSE updates were subsequently reduced to changes plus heartbeats.

Kubernetes manifests were rendered, not deployed to a cluster. No native non-OpenAI engine protocol, distributed collector, NCCL profiler, OIDC/RBAC, or full OTLP implementation is claimed. See [scope and roadmap](roadmap.md).
