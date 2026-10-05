# GPUP

GPUP is a local inference observability and benchmarking tool. A Go executable embeds the React dashboard and persists targets, request metadata, and runs in SQLite. The original `index.html` remains a simulated visual prototype; run the executable for the live application.

## Build and run

Install Go 1.22 or newer and Node.js 22 with npm. A C compiler is needed for native NVML support; the NVIDIA driver library is loaded at runtime.

```sh
make build
./bin/gpup serve
# Same dashboard server:
./bin/gpup ui
```

Open `http://127.0.0.1:7331`. The database defaults to the user configuration directory's `gpup/gpup.db`. Override it with `--db /path/to/gpup.db`. The binary includes the built frontend; Node is only needed during development/build.

```sh
./bin/gpup target add local --url http://127.0.0.1:8000/v1 --engine vllm \
  --model my-model --metrics-url http://127.0.0.1:8000/metrics
./bin/gpup status
./bin/gpup models
./bin/gpup requests
./bin/gpup gpu
./bin/gpup top
./bin/gpup discover
./bin/gpup doctor
```

For an authenticated inference endpoint, add `--api-key-env INFERENCE_API_KEY` and set that environment variable. Keys are resolved from the environment rather than persisted in SQLite. Target commands use the target ID or name shown by the CLI.

```sh
./bin/gpup bench local --model my-model --concurrency 1,2,4 \
  --duration 30s --max-tokens 128 --prompt 'Explain GPU memory bandwidth.' --save baseline
./bin/gpup bench local --model my-model --concurrency 1,2,4 \
  --duration 30s --max-tokens 128 --prompt 'Explain GPU memory bandwidth.' \
  --save optimized --compare baseline --max-throughput-drop 10 --max-latency-increase 20
./bin/gpup sweep local --model my-model --concurrency 1,2,4 --duration 30s
./bin/gpup profile local --duration 30s
./bin/gpup compare BASELINE_RUN_ID CANDIDATE_RUN_ID
```

`sweep` varies request concurrency; it does not change engine configuration. `profile` passively samples observations. Benchmark prompts are sent to the selected inference server but are not stored in the database. Check `gpup COMMAND --help` for current flags and comparison threshold units.

For CI, add `--compare baseline --fail-if 'throughput<-5%' --fail-if 'ttft-p99>+10%'`. Failed workloads and incompatible runs cannot pass comparison. Throughput gates prefer reported output tokens/sec and explicitly fall back to requests/sec when usage is unavailable. Latency percentiles use at most 10,000 uniformly sampled successes per point; mean and maximum remain exact. Each point records whether sampling occurred.

The CLI and daemon share SQLite. An already running daemon picks up target registration and completed CLI runs on its next collection cycle. Request metadata is bounded to 2,000 visible rows; it describes GPUP-generated requests, not every request received by an inference server. Local GPU telemetry is not automatically attributed to a remote target.

## Network access and collection

The default listener is loopback. For a remote listener, set `GPUP_API_TOKEN` and supply `--tls-cert` and `--tls-key` for TLS. Clients send `Authorization: Bearer TOKEN`. The dashboard offers a token input. `--read-only` disables mutating API operations. Avoid placing the token in command history; `--token` is available when required by an existing launcher. Reverse proxies must preserve Host and use HTTPS to the TLS-enabled backend so strict origin checks remain valid.

```sh
export GPUP_API_TOKEN='your-generated-secret'
./bin/gpup serve --listen 0.0.0.0:7331 --db /data/gpup.db \
  --dcgm-url http://127.0.0.1:9400/metrics
```

NVML collection requires a cgo build and a usable NVIDIA driver. Builds with `CGO_ENABLED=0`, including the supplied Docker image, use `nvidia-smi` fallback when that executable is available. Optional DCGM scraping supplements identified local devices; it is not distributed device discovery. Unsupported readings stay unavailable. Containers need NVIDIA Container Toolkit and appropriate device/utility access to collect GPU measurements. Inference targets can still be monitored without a local GPU.

## Development and deployment

`make test` builds the frontend and runs Go/frontend tests. `make check` adds Go vet and race checks. Start `gpup serve` and `make dev` in separate terminals for frontend development; Vite proxies API traffic to the local server.

Packaging examples live under [deploy](deploy): Docker/Compose, a Helm chart, and a loopback systemd service. Compose publishes only to host loopback and keeps SQLite in a named volume. Set `GPUP_API_TOKEN` before starting it:

```sh
docker compose -f deploy/docker/compose.yaml up --build
```

See [metric semantics](docs/metrics.md), [scope and roadmap](docs/roadmap.md), and the [MVP design](docs/superpowers/specs/2026-10-04-gpup-mvp-design.md). Hardware accuracy and collection overhead require validation on actual GPUs; deployment examples do not imply production certification.
