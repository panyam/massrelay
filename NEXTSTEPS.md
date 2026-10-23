# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. main is at v0.2.4, released and published to npm from CI.
- This run: converted the old checklist. Untracked items became #36 (embed `*pb.Room`), #37 (thin oneauth slice), #38 (validated reconnect), #39 (OTEL tracing); the persistent KeyStore item went onto #27. Done items dropped (git history has them).

## Across threads

- #27 (mcpkit bridge interop against a real relay) waits on panyam/mcpkit#1581. It also needs the relay binary to get a KeyStore; today `web/server/app.go` leaves it nil, so the binary can't validate any token.
- relay01 (relay01.excaliframe.com) is down; don't plan a deploy around it.
