# Inference observability delivery plan

Goal: deliver the specified dashboard through independently verifiable increments.

Architecture: a React interface reads normalized observations from a dedicated Go API backed by Prometheus. Benchmark execution is isolated from dashboard reads. Persistent task status belongs in Beads; this document defines implementation sequencing.

## 1. Reviewable visual prototype — delivered

Artifact: `index.html`. It runs without dependencies and shows explicitly simulated measurements. Verify navigation, filters, ranges, keyboard focus, and viewport containment. This prototype is a design artifact, not the production application.

## 2. Telemetry contracts and adapters

Create `backend/internal/inference/contracts.go`, `adapter.go`, `prometheus.go`, and `prometheus_test.go`. Define nullable observations, capabilities, provenance, and warnings as specified. Build fixture-driven vLLM and DCGM adapters; fixtures must include a counter reset, unavailable field, incompatible histograms, and ambiguous GPU attribution. Tests assert aggregate quantiles come from buckets and unknown values never become zero.

## 3. Bounded API

Create `backend/internal/inference/handler.go`, `cache.go`, and `handler_test.go`. Scaffold a Go service and register the five read endpoints with authentication middleware. Enforce role/deployment scope, query limits, timeout, cancellation, and response metadata. Verify authorization denial, malformed ranges, cache coalescing, stale data, and partial failure using fake datasource responses. Run the owning Go package tests before integration.

## 4. Production interface

Scaffold React with TypeScript and Vite under `frontend/`. Create `frontend/src/inference/InferenceDashboard.tsx`, `InferenceDashboard.css`, `api.ts`, `types.ts`, and `InferenceDashboard.test.tsx`. Port the prototype's layout into focused overview, GPU, benchmark, and chart components. Add typed fetching, URL filters, abortable requests, panel-level loading/errors, hidden-tab suspension, accessible chart data, and capability-aware units. Register the dashboard as the primary application route.

## 5. Browser and performance evidence

Create `frontend/e2e/inference-dashboard.spec.ts` using existing browser infrastructure where available. Exercise real rendered controls at 360px, 768px, and 1440px, including loading, empty, partial failure, stale, and keyboard flows. Scroll targets into view and assert resulting content. Store sanitized screenshots in `output/inference/`. Measure bundle size, mobile Web Vitals, refresh render time, and a 30-minute memory soak. Record measured values and environment in `docs/inference-verification.md`; do not mark targets passed without evidence.

## 6. Reproducible benchmark service

Create `backend/internal/inference/benchmark/manifest.go`, `worker.go`, `store.go`, and owning tests. Create `deploy/inference-benchmark-job.yaml` for isolated execution and `docs/inference-benchmarks.md` for exact pinned harness commands. Implement bounded launch, immutable manifests/artifacts, cancellation, authorization, and audit. Tests must prove incomparable runs are flagged and interrupted jobs cannot be reported complete. Execute the specification's workload matrix only against an explicitly configured authorized target; retain raw sanitized timings and quality evidence.

## Completion criteria

Each live increment records actual verification in its Beads issue. Release requires complete metric semantics, source provenance, browser acceptance, performance measurements, and reproducible benchmark artifacts. No commit, deployment, or external benchmark target is assumed from this design request.
