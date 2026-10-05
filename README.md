# GPUP

GPUP is a local inference observability and benchmarking tool for OpenAI-compatible inference servers and NVIDIA GPUs. Monitor requests and GPU telemetry, benchmark concurrency levels, and compare saved runs from a CLI or React dashboard.

A single Go executable serves the dashboard and stores targets, request metadata, and runs in SQLite. The original `index.html` is a separate, simulated visual prototype; use the executable for the live application.

## Quick start

Building from source requires Go 1.22 or newer, Node.js 22 with npm, and Make. Native NVML collection also requires a C compiler and a usable NVIDIA driver; the driver library is loaded at runtime. Node.js is only needed to build or develop the frontend.

```sh
git clone https://github.com/hawxxx/gpup.git
cd gpup
make build
./bin/gpup serve
```

Open <http://127.0.0.1:7331>. `./bin/gpup ui` starts the same server. Inference monitoring requires a running compatible endpoint; you can register one from another terminal while the dashboard is running. A local GPU is optional.

## Connect an inference server

Replace `my-model` with the model ID exposed by your server. The example assumes a vLLM server on port 8000:

```sh
./bin/gpup target add local --url http://127.0.0.1:8000/v1 --engine vllm \
  --model my-model --metrics-url http://127.0.0.1:8000/metrics
./bin/gpup target list
./bin/gpup status
./bin/gpup models
```

`--metrics-url` is optional. Integration uses OpenAI-compatible model discovery and streamed chat completions at the configured URL. Other engine labels, including SGLang, TensorRT-LLM, Triton, llama.cpp, Ollama, and TGI, require endpoints that implement that protocol. Native engine APIs and universal exporter coverage are deferred.

For an authenticated endpoint, set `INFERENCE_API_KEY` in the environment of each GPUP process that connects to it and add `--api-key-env INFERENCE_API_KEY` when registering the target. GPUP stores the variable name; it resolves the key from the environment and does not persist its value. Target commands accept the registered name or ID.

## Inspect requests and GPUs

```sh
./bin/gpup requests
./bin/gpup gpu
./bin/gpup top
./bin/gpup discover
./bin/gpup doctor
```

`top` opens the interactive terminal dashboard. `discover` finds and registers compatible local endpoints. `doctor` checks the local setup. Add `--json` to inspection commands for machine-readable output.

Request metadata covers GPUP-generated requests, with up to 2,000 visible rows. It does not capture all traffic received by an inference server. Local GPU telemetry is not automatically attributed to a remote target. Missing or unsupported measurements stay unavailable; opt-in demo data is visibly labeled.

## Benchmark and compare

Run the same workload before and after a change to your inference server:

```sh
./bin/gpup bench local --model my-model --concurrency 1,2,4 \
  --duration 30s --max-tokens 128 --prompt 'Explain GPU memory bandwidth.' --save baseline

./bin/gpup bench local --model my-model --concurrency 1,2,4 \
  --duration 30s --max-tokens 128 --prompt 'Explain GPU memory bandwidth.' \
  --save optimized --compare baseline --max-throughput-drop 10 --max-latency-increase 20
```

`--duration` applies to each concurrency point. Comparison thresholds are percentages: this example permits a 10% throughput drop and a 20% increase in E2E p95 latency. Comparisons require compatible workloads; failed workloads and incompatible runs cannot pass.

You can also sweep concurrency, capture passive telemetry, or compare existing runs:

```sh
./bin/gpup sweep local --model my-model --concurrency 1,2,4 --duration 30s
./bin/gpup profile local --duration 30s
./bin/gpup compare BASELINE_RUN_ID CANDIDATE_RUN_ID
```

`sweep` varies client concurrency without changing engine configuration. `profile` samples observations without generating a benchmark workload. Prompts are sent to the selected inference server but are not stored in the database.

For CI, add `--compare baseline --fail-if 'throughput<-5%' --fail-if 'ttft-p99>+10%'` to a matching benchmark command. Throughput gates prefer reported output tokens/sec and explicitly fall back to requests/sec when usage is unavailable. Latency percentiles use at most 10,000 uniformly sampled successful requests per point; mean and maximum remain exact, and each point records whether sampling occurred. See [metric semantics](docs/metrics.md) for timing definitions and measurement limits.

Use `./bin/gpup COMMAND --help` for command options.

## Configuration and remote access

| Setting | Default | Purpose |
| --- | --- | --- |
| `--db PATH` | User configuration directory's `gpup/gpup.db` | SQLite storage shared by the CLI and server |
| `serve --listen ADDRESS` | `127.0.0.1:7331` | Dashboard and API bind address |
| `GPUP_API_TOKEN` | Unset | API authentication; required for non-loopback binding |
| `serve --tls-cert PATH` / `--tls-key PATH` | Unset | Enable HTTPS; supply both files |
| `serve --read-only` | Disabled | Disable mutating API operations |
| `--dcgm-url URL` / `GPUP_DCGM_URL` | Unset | Optional DCGM exporter endpoint |

Use the same `--db` path for CLI commands and the server when overriding storage. A running server picks up target registrations and completed CLI runs on its next collection cycle.

For remote access, set `GPUP_API_TOKEN` and enable TLS with your certificate and private key:

```sh
export GPUP_API_TOKEN='your-generated-secret'
./bin/gpup serve --listen 0.0.0.0:7331 --db /data/gpup.db \
  --tls-cert /path/to/cert.pem --tls-key /path/to/key.pem \
  --dcgm-url http://127.0.0.1:9400/metrics
```

API clients send `Authorization: Bearer TOKEN`; the dashboard provides a token input. Inject the token through your environment or secret manager to avoid storing it in command history. `--token` is also available for existing launchers. Reverse proxies must preserve Host and use HTTPS to the TLS-enabled backend so origin checks remain valid.

## GPU collection

NVML collection requires a cgo build and a usable NVIDIA driver. Builds with `CGO_ENABLED=0`, including the supplied Docker image, use `nvidia-smi` fallback when that executable is available. Optional DCGM scraping supplements identified local devices; it is not distributed device discovery. Unsupported readings stay unavailable. Containers need NVIDIA Container Toolkit and appropriate device/utility access to collect GPU measurements. Inference targets can still be monitored without a local GPU.

## Development

| Command | What it does |
| --- | --- |
| `make build` | Install frontend dependencies, build the dashboard, and compile `bin/gpup` |
| `make test` | Build the frontend and run Go and frontend tests |
| `make check` | Build the frontend, run Go vet and race tests, and run frontend tests |
| `make dev` | Start the Vite development server |
| `make docker` | Build the `gpup:local` Docker image |

For frontend development, run `./bin/gpup serve` and `make dev` in separate terminals after building. Open the URL printed by Vite; it proxies `/api` requests to `http://127.0.0.1:7331`.

## Deployment

Run the following commands from the repository root. Docker builds the frontend and backend inside the image, so you do not need Go or Node.js installed on the host. Packaging also includes a [systemd service](deploy/systemd/gpup.service).

### Docker Compose

Requires Docker Engine and the Docker Compose plugin. Set `GPUP_API_TOKEN` from your secret manager or an existing token file outside the repository, then build and start GPUP:

```sh
export GPUP_API_TOKEN="$(cat /secure/gpup-token)"
docker compose -f deploy/docker/compose.yaml up --build -d
docker compose -f deploy/docker/compose.yaml logs -f gpup
```

Open <http://127.0.0.1:7331> and enter the token in the dashboard. Compose publishes only to host loopback and stores SQLite in the `gpup-data` named volume. Press Ctrl+C to stop following logs; the service keeps running.

Register an inference server at a URL reachable **from the container**, then run a benchmark:

```sh
docker compose -f deploy/docker/compose.yaml exec gpup \
  gpup --db /data/gpup.db target add inference \
  --url http://inference:8000/v1 --engine vllm --model my-model \
  --metrics-url http://inference:8000/metrics

docker compose -f deploy/docker/compose.yaml exec gpup \
  gpup --db /data/gpup.db status

docker compose -f deploy/docker/compose.yaml exec gpup \
  gpup --db /data/gpup.db bench inference --model my-model \
  --concurrency 1,2,4 --duration 30s --save baseline
```

Replace `inference`, port 8000, and `my-model` with your server's address and model ID. A container name such as `inference` resolves only when both containers share a Docker network; attach GPUP to the inference service's network in your Compose configuration. Container `127.0.0.1` refers to the GPUP container. For a host inference server, use `host.docker.internal` on Docker Desktop; on Linux, add an `extra_hosts` entry mapping `host.docker.internal` to `host-gateway`. The inference server must listen on an interface reachable from that network.

Always pass `--db /data/gpup.db` to container CLI commands so they use the server's database. For an authenticated inference endpoint, pass its key through the container environment and register the corresponding `--api-key-env` name; the server needs that variable too.

Stop the deployment with:

```sh
docker compose -f deploy/docker/compose.yaml down
```

The named volume remains available for the next start. `down --volumes` deletes the stored targets and runs.

For GPU collection, configure NVIDIA Container Toolkit on the host and enable the commented `gpus: all` setting in [compose.yaml](deploy/docker/compose.yaml). The image uses `nvidia-smi` fallback and needs NVIDIA utility access. This setup monitors GPUs visible to the container; inference endpoints can be monitored without GPU access.

### Kubernetes with Helm

Requires a Kubernetes cluster, `kubectl`, Helm, and a registry your cluster can pull from. The chart deploys GPUP; provision your inference server separately. Replace `registry.example.com/your-team/gpup` with your image repository:

```sh
docker build -f deploy/docker/Dockerfile \
  -t registry.example.com/your-team/gpup:0.1.0 .
docker push registry.example.com/your-team/gpup:0.1.0

kubectl create namespace gpup
kubectl -n gpup create secret generic gpup-token \
  --from-file=token=/secure/gpup-token

helm upgrade --install gpup deploy/helm/gpup --namespace gpup \
  --set image.repository=registry.example.com/your-team/gpup \
  --set image.tag=0.1.0

kubectl -n gpup rollout status deployment/gpup
kubectl -n gpup port-forward service/gpup 7331:7331
```

Use an existing token file containing only the token, without a trailing newline, or provision the Secret through your secret manager. If the namespace already exists, skip its creation. For a private registry, configure image pull credentials for the workload's default ServiceAccount before installing.

Keep port-forward running, open <http://127.0.0.1:7331>, and enter the same token. In another terminal, register your inference service using its Kubernetes DNS name:

```sh
kubectl -n gpup exec deployment/gpup -- \
  gpup --db /data/gpup.db target add inference \
  --url http://vllm.inference.svc.cluster.local:8000/v1 \
  --engine vllm --model my-model \
  --metrics-url http://vllm.inference.svc.cluster.local:8000/metrics

kubectl -n gpup exec deployment/gpup -- \
  gpup --db /data/gpup.db bench inference --model my-model \
  --concurrency 1,2,4 --duration 30s --save baseline

kubectl -n gpup logs deployment/gpup
```

Replace `vllm`, namespace `inference`, port 8000, and `my-model` with your inference Service and model. Its network policy must allow traffic from GPUP. Authenticated inference endpoints also need their key environment variable injected into the GPUP pod; the chart's `gpup-token` Secret authenticates GPUP's API, not the inference server.

The chart runs one replica with SQLite on a 1 GiB PVC and uses Recreate updates. It requires a default StorageClass unless you set `--set persistence.storageClass=YOUR_STORAGE_CLASS`. `--set persistence.enabled=false` uses ephemeral storage and loses data when the pod is replaced.

The Service is ClusterIP. Port-forward provides local access; external ingress and TLS require additional configuration. The chart does not provision GPU access, an inference engine, or DCGM Exporter. GPU monitoring requires NVIDIA runtime/device access and scheduling GPUP on the GPU node. A remote DCGM URL supplements already identified local GPUs; it does not discover a GPU fleet. Independent GPUP instances do not aggregate into a cluster dashboard.

See the [Helm guide](deploy/helm/gpup/README.md) and [chart values](deploy/helm/gpup/values.yaml) for token Secret overrides, storage, resource limits, DCGM, and read-only mode.

## Scope and further reading

GPUP currently runs as a single local instance. Distributed collectors, cluster-wide aggregation, remote GPU discovery, multiuser RBAC/OIDC, automatic engine tuning, and AMD/Intel GPU support are deferred. Hardware accuracy and collection overhead require validation on actual GPUs; deployment examples do not imply production certification.

- [Metric semantics](docs/metrics.md): timing definitions, telemetry sources, and measurement limits
- [Scope and roadmap](docs/roadmap.md): implemented capabilities and deferred work
- [MVP design](docs/superpowers/specs/2026-10-04-gpup-mvp-design.md): architecture and behavior
