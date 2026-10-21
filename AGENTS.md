# Repository Guidelines

## Project Structure & Module Organization

GPUP is an inference observability and benchmarking tool: one Go executable provides the CLI, the terminal dashboard, and an HTTP server that embeds a React dashboard. State lives in a local SQLite workspace. See [docs/architecture.md](docs/architecture.md) for diagrams of each code path.

- `cmd/gpup` — Cobra CLI (`serve`, `target`, `bench`, `sweep`, `compare`, `runs`, `top`, `discover`, `doctor`, ...) and the Bubble Tea TUI.
- `internal/app` — application state, the 5-second poll loop, benchmark jobs, snapshot history, and findings.
- `internal/api` — chi router, REST and SSE endpoints, Prometheus `/metrics`, and authentication (`security.go`).
- `internal/bench` — closed-loop and open-loop benchmark execution, percentiles, goodput, and error classes.
- `internal/analysis` — knee and saturation detection, run comparison, and regression gates.
- `internal/engine` — OpenAI-compatible client (model discovery, streamed chat completions) and engine exporter scraping.
- `internal/gpu` — NVML (cgo), `nvidia-smi` fallback, and optional DCGM supplements.
- `internal/storage` — SQLite documents and snapshots; `internal/promtext` — exposition line parser.
- `web/` — React 19 + TanStack + uPlot dashboard; `web/dist` is committed and embedded by `web/embed.go`.
- `benchmarks/fixture` — synthetic OpenAI-compatible endpoint for tests and screenshots. Its timings are not engine measurements.
- `deploy/` — Dockerfile, Compose, Helm chart, and systemd unit.
- `docs/` — [metric semantics](docs/metrics.md), [security model](docs/security.md), [architecture](docs/architecture.md), research, specs, and plans.
- `index.html` — the original simulated visual prototype. It is not the application.

## Build, Test, and Development Commands

Requirements: Go 1.22+ (CI uses 1.26), Node.js 22 with npm, Make, and a C compiler for NVML and the race detector.

- `make build` — `npm ci`, build the dashboard into `web/dist`, then build `bin/gpup`.
- `make test` — `go test ./...` and `npm --prefix web test`.
- `make check` — `go vet ./...`, `go test -race ./...`, and the web tests.
- `CGO_ENABLED=0 go test ./...` — the configuration used by the Docker image (no native NVML).
- `npm --prefix web run build` — type-check and rebuild `web/dist`; commit the result with frontend changes.
- `make dev` — Vite dev server on 127.0.0.1; run `./bin/gpup serve` separately for the API.
- `go run ./benchmarks/fixture -capacity 4 -tokens 32` — local endpoint on 127.0.0.1:17332 for manual and browser checks.
- Fuzzing: `go test ./internal/engine -run '^$' -fuzz FuzzGenerateStream -fuzztime 30s` and `go test ./internal/promtext -run '^$' -fuzz FuzzParseLine -fuzztime 30s`.

CI additionally runs `gofmt`, `govulncheck`, `npm audit --audit-level=high`, `helm lint`, `helm template`, and a read-only, non-root Docker smoke test.

## Coding Style & Naming Conventions

Go code is `gofmt`-formatted, uses standard-library idioms, and keeps comments for non-obvious reasoning. Frontend code is TypeScript with two-space indentation and compact single-line JSX; match the surrounding density rather than reformatting files. Use camelCase identifiers and lowercase, hyphenated CSS classes. Colors come from theme tokens in `style.css` (`--text`, `--muted`, `--green`, `--chart-*`) so both themes keep WCAG AA contrast.

Never fabricate measurements: unknown readings stay `null`/absent and render as `—`; demo data stays labeled. Every new metric states its source, unit, and unavailability behavior in `docs/metrics.md`. Settings that change benchmark results belong in `bench.Config` so comparisons can reject incompatible runs.

Name design and planning documents with date prefixes, such as `YYYY-MM-DD-feature-design.md`. Keep Mermaid diagrams in `docs/architecture.md` in step with the code paths they describe.

## Testing Guidelines

Add Go tests beside the package (`*_test.go`) using `httptest` servers or fake generators instead of real hardware, and run `go test -race`. Web tests use Vitest and Testing Library (`web/src/*.test.ts(x)`). Security-relevant changes need regression tests in `internal/api` or `internal/engine`.

For interface changes, run the fixture and `gpup serve`, then check every page in a real browser at 320, 375, 768, 1024, 1440, and 1920 px in both themes: no page-level horizontal overflow, keyboard focus visible, no console errors, and no axe-core WCAG 2.2 AA violations.

## Commit & Pull Request Guidelines

Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, `ci:`, ...) with concise, imperative subjects. PR titles are checked in CI and become the squash commit that release-please uses for versioning and `CHANGELOG.md`; do not edit versions or the changelog by hand.

Pull requests should explain the problem, resulting behavior, and verification performed. Link relevant issues and design documents; include screenshots for interface changes. State which results came from the synthetic fixture and which from real engines or GPUs.
