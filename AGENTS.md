# Repository Guidelines

## Project Structure & Module Organization

gpup is an inference and GPU observability dashboard. The current implementation is a dependency-free visual prototype, not a live telemetry service.

- `index.html` contains the complete interface, inline CSS, JavaScript, and visual assets. Measurements are simulated.
- `docs/specs/2026-10-04-inference-observability-design.md` defines product behavior, metric semantics, architecture, and acceptance criteria.
- `docs/plans/2026-10-04-inference-observability.md` describes delivery increments and verification expectations.

The proposed production stack uses React, a Go API, and Prometheus with vLLM and NVIDIA DCGM Exporter. These modules are not implemented yet. No separate source, test, or asset directories currently exist.

## Build, Test, and Development Commands

- Open `index.html` directly in a browser for a quick preview.
- Run `python3 -m http.server 8000` from the repository root, then visit `http://localhost:8000` for local HTTP testing.

There is no build step, package manifest, automated test command, or configured linter. Do not assume `npm test` or Go commands apply until the relevant tooling exists.

## Coding Style & Naming Conventions

Follow the existing HTML structure and two-space indentation. Keep JavaScript identifiers descriptive and use camelCase; use lowercase, hyphenated CSS classes. Preserve semantic HTML, accessible control labels, keyboard focus indicators, and responsive layouts. Avoid unrelated reformatting of the compact inline CSS.

Name design and planning documents with date-prefixed, descriptive filenames, such as `YYYY-MM-DD-feature-design.md`. Update specifications when behavior or metric contracts change.

## Testing Guidelines

Verification currently uses manual browser checks. Exercise navigation, filters, time ranges, and keyboard focus. Check narrow and wide viewports for containment, scrolling, and readable charts. Check the browser console for errors and keep simulated data visibly labeled.

No testing framework or coverage threshold is configured. For future live increments, follow the browser, metric, performance, and benchmark acceptance criteria in the design documents.

## Commit & Pull Request Guidelines

This checkout has no Git metadata, so no historical commit convention can be verified. Use concise, imperative commit subjects describing the change.

Pull requests should explain the problem, resulting behavior, and verification performed. Link relevant issues and design documents; include screenshots for interface changes. Clearly distinguish prototype changes from implemented telemetry integration.
