# Next Steps

## Immediate (PR #10 in progress)

- [x] Merge PR #8 (embed PeerInfo, map peers, Timestamp, typed TS)
- [x] OTEL metrics instrumentation
- [x] Security hardening (origin allowlist, conn limits, rate limiting, Guard)
- [x] Docker packaging + production/dev compose stacks
- [x] Deployment scripts (setup-host.sh, update-pool.sh)
- [x] Structured logging (slog + JSON stdout)
- [x] Grafana dashboard provisioning
- [ ] Merge PR #10, deploy first relay to IONOS VPS
- [ ] Consider embedding `*pb.Room` in `CollabRoom`

## Short-term

- [ ] **Issue #9**: Migrate TS to canonical protobuf-es Message types with `fromJson()`/`toJson()` at the boundary
- [ ] **Issue #2**: Replace manual broadcast with `gocurrent.FanOut` — **blocked on [gocurrent#1](https://github.com/panyam/gocurrent/issues/1)**
- [ ] **Issue #14**: Extract sessionStore into massrelay as generic cross-tab session persistence
- [ ] Type the `SyncAdapter` interface more strictly
- [ ] Add `tsc --noEmit` to CI

## Medium-term

- [ ] **Issue #4**: Client-side relay pool with health probing (relay pool manifest, latency probing, auto-reconnect)
- [ ] **Issue #6**: Distributed relay architecture (memberlist + gRPC)
- [ ] Auto-reconnect with session validation
- [ ] OTEL tracing (message flow through relay — spans for join, broadcast, leave)
- [ ] Lift `web/middleware` hardening to servicekit
