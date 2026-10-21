# Security model

GPUP is a single-operator tool. It sends traffic to inference endpoints that its users register, and it reads local GPU telemetry. This document records what it protects, what it assumes, and where the controls live. See [architecture](architecture.md#api-authentication-and-browser-communication) for the request flow.

## Assets

- Inference credentials. GPUP stores only the name of an environment variable (`apiKeyEnv`), never its value. The value is read per request and sent as a bearer token.
- The API token (`GPUP_API_TOKEN` or `--token`), which grants every API operation.
- Benchmark prompts. They are sent to the target but never persisted; runs store a SHA-256 workload hash.
- The SQLite workspace (mode `0600`, directory `0700`) with targets, runs, request metadata, and telemetry snapshots.

## Trust boundaries and controls

| Threat | Control | Code |
| --- | --- | --- |
| Remote access without credentials | Non-loopback binds require a token. Without a token, API requests must carry a loopback `Host` header, which blocks DNS rebinding. | `internal/api/security.go` |
| Cross-site request forgery | Mutations reject an `Origin` that differs from the request host and scheme. The session cookie is `HttpOnly` and `SameSite=Strict`. | `Secure` |
| Session cookie replay as a bearer token | The cookie holds `HMAC-SHA256(token)`, not the token. A cookie value is not accepted in `Authorization`. | `sessionValue` |
| Credential leak to a third-party exporter | The target's bearer token is sent only to URLs with the same scheme, host, and port as the target URL. A metrics URL on another origin is scraped without it. | `engine.sameOrigin` |
| Credentials embedded in URLs | Target and metrics URLs must be HTTP(S) without user info, query, or fragment. | `engine.validURL` |
| Server-side request forgery to cloud metadata | Outbound engine connections refuse link-local and unspecified addresses after DNS resolution, which covers `169.254.169.254` and `fe80::/10`. | `engine.refuseLinkLocal` |
| Oversized or hostile responses | Model lists are capped at 4 MiB, SSE streams at 32 MiB with 1 MiB lines, exporter scrapes at 4 MiB, and error bodies at 4 KiB, of which 200 characters are shown. | `internal/engine` |
| Oversized requests and slow clients | JSON bodies are capped at 256 KiB with unknown fields rejected. The server sets header, read, and idle timeouts, and SSE writes have 5-second deadlines. At most 16 live streams are served. | `internal/api/server.go`, `cmd/gpup` |
| Unbounded load from one user | Benchmarks allow one job at a time, 32 points, 256 concurrency, 10,000 req/s, and one hour per point. | `bench.Config.Validate` |
| Script injection in the dashboard | React escapes rendered text. The CSP allows scripts only from the GPUP origin. | `Secure` |
| SQL injection | All queries use placeholders. | `internal/storage` |
| Writes from viewers | `--read-only` rejects every mutation. | `Secure` |
| Container privileges | The image runs as UID 10001 and CI smoke-tests it with a read-only root filesystem and no capabilities. The Helm pod drops all capabilities, disables privilege escalation, uses a read-only root filesystem and the `RuntimeDefault` seccomp profile, and does not mount a service-account token. | `deploy/` |

## Assumptions and accepted risks

- Anyone holding the API token can register endpoints, which makes GPUP send requests to any address that is not link-local, including private networks reachable from the host. Treat the token as an operator credential. Use `--read-only` for shared viewing.
- Engine error text (up to 200 characters) and target health appear in snapshots, so authenticated readers can see short responses from registered endpoints.
- Without TLS (`--tls-cert`, `--tls-key`) or a TLS-terminating proxy, the token travels in clear text. Bind remotely only behind TLS.
- IPv6 unique-local metadata addresses, such as AWS `fd00:ec2::254`, are not link-local and are not blocked. Block them with network policy where needed.
- The GPU collector runs `nvidia-smi` from `PATH` with fixed arguments; no user input reaches the command line.
- `/healthz` is unauthenticated and reveals the version.

## Verification

Regression tests cover token and cookie handling, origin checks, read-only mode, DNS rebinding, credential scoping by origin, link-local refusal, and request validation (`go test ./internal/api ./internal/engine`). Fuzz targets exercise SSE parsing and exposition lines:

```sh
go test ./internal/engine -run '^$' -fuzz FuzzGenerateStream -fuzztime 30s
go test ./internal/promtext -run '^$' -fuzz FuzzParseLine -fuzztime 30s
```

CI runs `govulncheck` and `npm audit --audit-level=high`. Report vulnerabilities privately to the maintainers rather than in a public issue.
