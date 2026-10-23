# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `CollabClient` takes a `token` option and sends it as the `token` query
  parameter, so a browser can join a relay running with JWT auth.
- `onAppMessage` gets a fourth argument, `toClientId`: the receiver's id for an
  addressed message, empty for a room-wide one. Existing three-argument
  callbacks keep working.

### Fixed
- `CollabClient` escapes the session id in the WebSocket URL. A room id with a
  `/` in it, such as `design:demo/board.kicad_sch`, used to break the route and
  surface only as `WebSocket error`.
- `CollabClient` no longer `console.log`s every event. Use `onEvent` to trace.
- The npm tarball no longer ships `dist/*.test.*`. The build uses
  `tsconfig.build.json`, which excludes tests, and CI checks the packed file
  list.

### Changed
- oneauth v0.3.0 (was v0.1.36). No code changes were needed, since massrelay no
  longer uses `admin.AppQuota`. The Go floor rises to 1.26.9.

## [0.2.2] - 2026-10-23

### Fixed
- `import { CollabClient } from "@panyam/massrelay"` resolves. The `exports`
  map had subpaths (`./client`, `./engine`, ...) and no `"."` entry, so the
  package root threw `ERR_PACKAGE_PATH_NOT_EXPORTED`, and `main` pointed at the
  generated model file. A new `src/index.ts` re-exports the client, engine,
  sync, crypto, peer and URL helpers and the model types. The subpaths are
  unchanged. Generated service stubs stay on `./services`.

## [0.2.1] - 2026-10-23

Security patch release. The Docker image builds again, and the binary picks
up patched dependencies (PR 28).

### Fixed
- The Docker image builds again. The Dockerfile used `golang:1.25-alpine`
  while `go.mod` has required Go 1.26.4 since April, so every build failed at
  `go mod download`. It now builds with `golang:1.26-alpine` (Go 1.26.10),
  which also carries the stdlib `net/http` and `crypto/x509` fixes.

### Security
- Go: golang.org/x/crypto v0.58.0, golang.org/x/net v0.61.0,
  google.golang.org/grpc v1.84.0, otel v1.47.0 (prometheus exporter v0.69.0),
  gorilla/websocket v1.5.3. Clears the Go Dependabot alerts.
- TS (dev only): vitest 4.1.12 with vite 8, plus patched postcss, nanoid,
  source-map-js and picomatch. `npm audit` reports 0.

## [0.2.0] - 2026-10-23

Full write-up in [docs/releases/v0.2.0.md](docs/releases/v0.2.0.md).

### Fixed
- The relay binary accepts WebSockets again. `main.go` wraps every route in
  servicekit's `RequestLogger`, whose status recorder hid `http.Hijacker`
  before servicekit v0.1.7, so every `/ws/` upgrade in v0.1.0 and v0.1.1
  failed with a 400 (PR 26).
- The WebSocket stream sets `CollabAction.client_id` from the connection
  instead of trusting the client's value, and rejects any action other than
  `JoinRoom` before the join. Before this, a connection could send updates or a
  `LeaveRoom` as another peer. The TS client is unaffected (PR 24).
- A second `JoinRoom` on one connection gets `ErrorEvent{ALREADY_JOINED}`.
  It used to add a second client that the stream forgot, which then stayed
  in its room for good (PR 26).

### Added
- `AppMessage.to_client_id` addresses one peer. The relay delivers to that
  client alone and answers the sender with `ErrorEvent{PEER_NOT_FOUND}` when it
  is not in the room. Empty keeps the room-wide fan-out (issue 23).
- `PeerInfo.identity` (`PeerIdentity{subject, client_id}`), filled from the
  connection's validated JWT. Absent without a token, and not settable from
  `JoinRoom`.
- `rooms` JWT claim (string or array; exact session IDs or `prefix*`). A join
  outside it gets `ErrorEvent{ROOM_FORBIDDEN}`.
- Go: `CollabService.DeliverAppMessage` for embedding servers, with
  `ErrPeerNotFound` / `ErrRoomNotFound`; `services.Principal` /
  `WithPrincipal` to attach an identity to in-process joins.
- TS: `sendAppMessage(kind, payload, { to })`; `onAppMessage` gets the sender's
  `fromClientId` as a third argument.
- `npm run test:interop` / `make test-interop`, plus a CI job, running the TS
  client against the real relay binary.

### Changed
- servicekit v0.1.3 to v0.1.7, which also brings v0.1.6's graceful shutdown
  change (request contexts are cancelled before the drain).
- CI runs on Node 22.

### Removed
- The `max_rooms` and `max_msg_rate` claims, which were parsed into
  `RelayClaims` and never enforced (issue 25).

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
