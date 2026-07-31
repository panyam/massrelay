# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.1] - 2026-07-31

### Added
- `AppMessage { kind, payload }` — a generic, app-defined pub-sub message added to
  the collab protocol (`CollabAction`/`CollabEvent` oneofs). The relay fans it out
  to the room unchanged, with **no server-side state and no editor semantics**, so
  any application can use massrelay as a message bus without adopting the
  Scene/Text editor protocol. The TypeScript client gains `sendAppMessage(kind,
  payload)` and an `onAppMessage(kind, payload)` callback. Backward-compatible
  (additive oneof fields).

## [0.1.0] - 2026-07-29

First release since v0.0.11. Full write-up in
[docs/releases/v0.1.0.md](docs/releases/v0.1.0.md).

### Breaking

- oneauth 0.0.x to 0.1.x `apiauth`/`keys` API migration: `TokenQueryParam` to
  `LegacyQueryParamBearer`, `GetUserIDFromAPIContext` to
  `GetSubjectFromAPIContext`, `keys.RegisterKey` to `keys.PutKey`.
- Go 1.26.4 minimum (was 1.26.1), required by oneauth v0.1.36.
- TypeScript client moves to canonical protobuf-es message types for
  `CollabEvent` (issue 9).

### Added

- OpenTelemetry metrics (Prometheus + OTLP), local Grafana LGTM stack, and
  dashboards (PR 10).
- Relay-side JWT auth middleware on oneauth with multi-tenant KeyStore and
  subject deny list (issue 12, PR 11).
- Per-subject rate limiting via `RELAY_PER_SUB_RATE` and `SubjectKeyFunc`
  (issue 14).
- Token-gated admin API (`/admin/status`, `/admin/rooms`).
- Security middleware: origin allowlist, CORS, trusted-proxy IP extraction,
  panic recovery, connection limits.
- Structured slog JSON logging keyed by `component`.
- Docker + Caddy deployment stack, host bootstrap and DNS scripts, pool
  management scripts.
- PRM endpoint RFC 9728 `/.well-known/oauth-protected-resource` (PR 18).
- `make audit` (govulncheck + gosec + gitleaks) and `tsc --noEmit` in CI.

### Changed

- Proto refactor: embed `PeerInfo` in `CollabClient`, move `Metadata` to
  `PeerInfo`, extract shared `Room` message (PR 7, PR 8).
- Graceful shutdown via servicekit `ListenAndServeGraceful` (issue 17).
- Hardening middleware lifted into `servicekit/middleware`.
- Dependency refresh: oneauth v0.1.36, servicekit v0.1.3, gocurrent v0.1.1,
  goutils v0.1.13, `@panyam/servicekit-client` ^0.0.7.

[0.1.0]: https://github.com/panyam/massrelay/releases/tag/v0.1.0
