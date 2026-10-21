# Architecture

GPUP is one Go executable. The same binary runs CLI commands, the terminal dashboard (`gpup top`), and the HTTP server that embeds the React dashboard. Every process opens the same SQLite workspace, so a CLI benchmark run while `gpup serve` is running appears in the dashboard after the next poll.

The diagrams below follow the code paths in `cmd/gpup`, `internal/*`, and `web/src`. Package names are given so each box can be traced to its source.

## System overview

```mermaid
flowchart LR
  subgraph clients[Clients]
    browser[React dashboard<br/>web/src]
    cli[CLI and TUI<br/>cmd/gpup]
    prom[Prometheus scraper]
  end
  subgraph gpup[gpup executable]
    api[HTTP API and static assets<br/>internal/api]
    app[App state, poll loop, jobs<br/>internal/app]
    bench[Benchmark engine<br/>internal/bench]
    analysis[Knee, saturation, comparison<br/>internal/analysis]
    engine[OpenAI-compatible client<br/>and exporter scraper<br/>internal/engine]
    gpu[GPU collector<br/>internal/gpu]
    store[(SQLite WAL<br/>internal/storage)]
  end
  subgraph external[Measured systems]
    server[Inference server<br/>/v1/models, /v1/chat/completions]
    exporter[Engine exporter<br/>/metrics]
    nvml[NVML or nvidia-smi]
    dcgm[DCGM exporter, optional]
  end
  browser -- REST + SSE --> api
  prom -- GET /metrics --> api
  cli --> app
  api --> app
  app --> bench --> engine
  app --> analysis
  app --> engine
  app --> gpu
  app <--> store
  engine -- streamed requests --> server
  engine -- scrape --> exporter
  gpu --> nvml
  gpu -. supplements XID and ECC .-> dcgm
```

## Live data flow

`App.Poll` runs once at startup and then every 5 seconds. Each change to state increments a revision counter; the SSE handler checks it once per second and sends a full snapshot only when it changed, with a heartbeat comment every 15 seconds otherwise.

```mermaid
flowchart TD
  tick[5 s ticker] --> refresh[Reload targets, runs, and the newest<br/>request chunks from SQLite]
  refresh --> collect[Collect local GPUs<br/>3 s deadline]
  refresh --> probe[Probe up to 8 targets concurrently<br/>model discovery and exporter scrape, 2 s each]
  collect --> merge[Merge into snapshot state<br/>findings, history point, revision++]
  probe --> merge
  requests[Benchmark request results] --> ring[Request ring, 2,000 rows<br/>chunk gaps stripped] --> merge
  merge --> persist[Append snapshot row<br/>prune: 24 h snapshots, 200 runs]
  merge --> sse[GET /api/v1/stream<br/>at most 1 snapshot/s per client, 16 clients]
  sse --> ui[Dashboard re-renders]
```

## Target registration and discovery

```mermaid
sequenceDiagram
  autonumber
  actor User
  participant API as API or CLI
  participant App as app.App
  participant Engine as engine.New
  participant DB as SQLite
  participant Server as Inference server
  User->>API: POST /api/v1/targets or gpup target add
  API->>App: AddTarget(target)
  App->>Engine: validate URL, metrics URL, engine label
  Engine-->>App: error for credentials, query, fragment, or unknown engine
  App->>DB: Put("target", id)
  App-->>API: 201 target with status "unknown"
  Note over App,Server: Next poll
  App->>Server: GET /v1/models (2 s)
  Server-->>App: model IDs, or error recorded as "unreachable"
  opt metrics URL configured
    App->>Server: GET metrics URL (2 s)
    Note right of App: Bearer token only when the metrics URL<br/>shares the endpoint origin
  end
  Note over User,Server: gpup discover / POST /api/v1/discover
  loop ports 8000, 30000, 11434, 8080, 1234, 9000 on 127.0.0.1
    App->>Server: GET /v1/models (350 ms)
    alt answers
      App->>Server: GET /metrics (500 ms)
      App->>App: AddTarget(local-PORT), metrics URL kept only<br/>if a recognized engine series is present
    end
  end
```

Every outbound connection from `internal/engine` refuses link-local and unspecified addresses after DNS resolution, so a target cannot reach instance-metadata services such as `169.254.169.254`.

## Benchmark execution

```mermaid
sequenceDiagram
  autonumber
  actor User
  participant API as API or CLI
  participant App as app.App
  participant Bench as bench.RunBenchmark
  participant Server as Inference server
  participant DB as SQLite
  User->>API: POST /api/v1/benchmarks or gpup bench
  API->>App: StartBenchmark(config)
  App->>App: Validate, reject if another job runs,<br/>add running summary (prompt removed)
  App-->>API: 202 run ID
  App->>Bench: goroutine with cancellable context
  loop each concurrency value or request rate
    alt closed loop
      Bench->>Server: N workers, next request when previous ends
    else open loop
      Bench->>Server: Poisson arrivals at the rate, at most N in flight
    end
    Server-->>Bench: SSE chunks, usage, [DONE]
    Bench->>App: onResult(request), progress, request ring
    Note over Bench: Warm-up requests are shown but not measured.<br/>Requests cut off by the deadline count as Incomplete.
    Bench->>Bench: percentiles, throughput, goodput, error classes
  end
  Bench-->>App: run with points
  App->>App: DetectKnee or SaturationRate, findings,<br/>attach local GPU samples per point
  App->>DB: Put("run"), Put("requests")
  App->>App: job status complete, cancelled, or error
  opt Cancel
    User->>API: DELETE /api/v1/benchmarks/{id}
    API->>App: CancelJob, context cancelled
    Bench-->>App: partial points, status "cancelled"
  end
```

## GPU telemetry and fallback

```mermaid
flowchart TD
  start[Collector.Collect, 3 s deadline] --> native{NVML loaded at startup?}
  native -- yes --> nvml[Read NVML devices]
  native -- no --> smi
  nvml -- error --> smi[nvidia-smi CSV query]
  nvml -- ok --> dcgmq
  smi --> reasons[Separate clock event reasons query,<br/>older field name as fallback]
  reasons --> dcgmq{DCGM URL configured?}
  smi -- error --> unavailable[Return no devices and<br/>'NVIDIA GPU collection unavailable']
  dcgmq -- no --> done[Devices; unsupported readings stay null]
  dcgmq -- yes --> dcgm[Scrape DCGM exporter, size-bounded,<br/>match by UUID: XID and ECC only]
  dcgm --> done
  dcgm -- error --> partial[Devices plus collector error 'DCGM: ...']
```

GPU readings describe the host running GPUP. They are never attributed to a remote target.

## API authentication and browser communication

```mermaid
stateDiagram-v2
  [*] --> Headers: every response gets nosniff, no-referrer, CSP
  Headers --> Static: path outside /api/v1 and /metrics
  Static --> [*]: embedded asset or index.html
  Headers --> TokenMode: API request
  state TokenMode <<choice>>
  TokenMode --> LoopbackHost: no token configured
  TokenMode --> Bearer: token configured
  LoopbackHost --> Rejected403: Host is not loopback (DNS rebinding)
  LoopbackHost --> Method: loopback Host
  Bearer --> Rejected401: header or cookie does not match
  Bearer --> SetCookie: Authorization matches
  Bearer --> Method: gpup_session cookie matches HMAC(token)
  SetCookie --> Method: HttpOnly, SameSite=Strict session for SSE
  Method --> Handler: GET, HEAD, OPTIONS
  Method --> ReadOnly403: mutation in --read-only mode
  Method --> OriginCheck: POST or DELETE
  OriginCheck --> Rejected403: Origin host or scheme differs
  OriginCheck --> Handler: same origin or no Origin header
  Handler --> [*]
```

Binding to a non-loopback address requires `GPUP_API_TOKEN`. The dashboard keeps a token entered by the user in `sessionStorage`, sends it as a bearer header, and the first authenticated response sets the session cookie that `EventSource` uses for `/api/v1/stream`.

## Comparison and regression gates

```mermaid
flowchart TD
  start[gpup bench --compare BASELINE<br/>or gpup compare A B] --> compat{Same model, prompt hash, max tokens,<br/>duration, warm-up, and load model?}
  compat -- no --> incompatible[Error: incompatible workload]
  compat -- yes --> status{Both runs complete and<br/>no point has errors?}
  status -- no --> refuse[Error: cannot compare]
  status -- yes --> match[Match points by concurrency,<br/>or by offered rate for open loop]
  match --> changes[Per point: throughput change, preferring output tok/s,<br/>E2E p95 change, TTFT p99 change]
  changes --> gates{Gates}
  gates --> tput[throughput below -max drop]
  gates --> lat[E2E p95 above +max increase]
  gates --> ttft[ttft-p99 rule]
  tput & lat & ttft --> exit[Non-zero exit on the first regression]
  gates -. independent of baseline .-> goodput[goodput rule: every candidate point<br/>at or above the target share]
  goodput --> exit
```

## Process lifecycle

```mermaid
stateDiagram-v2
  [*] --> Validate: gpup serve
  Validate --> Exit: non-loopback bind without token, or partial TLS flags
  Validate --> Open: app.New
  Open --> Exit: SQLite open or schema error
  Open --> Restore: load targets, 200 runs, 2 request chunks, 600 history points
  Restore --> Discover: no targets and not read-only
  Restore --> Polling: otherwise
  Discover --> Polling: App.Start, first poll immediately
  Polling --> Serving: ListenAndServe(TLS)
  Serving --> Stopping: SIGINT or SIGTERM
  Stopping --> Drain: App.Stop cancels polls, jobs, and SSE streams
  Drain --> Close: http.Server.Shutdown, 5 s, then Close
  Close --> [*]: wait for goroutines, close NVML, close SQLite
```

CLI commands use the same `app.New` and `App.Close` path without the HTTP server. `gpup bench` starts the poll loop so GPU samples can be attached to points, and cancels its job on interrupt.
