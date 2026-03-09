# Next Steps

## Immediate (from current PR #8)

- [ ] Merge PR #8 (embed PeerInfo, map peers, Timestamp, typed TS)
- [ ] Consider embedding `*pb.Room` in `CollabRoom` — now viable since `peers` is a map and `created_at` is Timestamp. Remaining friction: `Clients map[string]*CollabClient` vs `Peers map[string]*PeerInfo` (CollabClient wraps PeerInfo + SendCh/BrowserId)

## Short-term

- [ ] **Issue #9**: Migrate TS to canonical protobuf-es Message types with `fromJson()`/`toJson()` at the boundary. Enables exhaustive oneof switch, validates incoming messages, prepares for binary transport.
- [ ] **Issue #2**: Replace manual broadcast with `gocurrent.FanOut`
- [ ] Type the `SyncAdapter` interface more strictly (currently `Record<string, unknown>` payloads)
- [ ] Add `tsc --noEmit` to CI to catch type errors in TS without relying on test execution

## Medium-term

- [ ] **Issue #4**: Client-side relay pool with health probing
- [ ] **Issue #6**: Distributed relay architecture (memberlist + gRPC)
- [ ] Auto-reconnect with session validation
