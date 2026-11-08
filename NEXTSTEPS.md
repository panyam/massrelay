# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.4 released 2026-11-08 (GitHub release, npm `latest`, Go proxy): in-session token refresh (#13, PR #67), `make exercise-prod` (#69, PR #70), and v0.3.3-client compatibility (PR #71).
- Active mission #72 (`mission_revoke`), opened 2026-11-08: an admin deny call ends a user's open connections within a second. Next ready: #73 (P1, admin deny/allow endpoints and `make exercise-revoke`), then #63 (P1, ending the streams). #68 closed 2026-11-08. Other P3: #37 (oneauth slice; pushdown panyam/oneauth#456), #39, #36.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI.
- This run: no threads to prune; PRs #67, #70 and #71 merged, and their worktrees and branches are gone.

## Across threads

- mcpkit's Go side (`experimental/ext/rooms/massrelay/go.mod`) pins massrelay exactly (v0.3.3 as of 2026-11-08). Bumping it to v0.3.4 picks up ReAuth; that's mcpkit work. Its npm side (`^0.3.x`) gets 0.3.4 on its own.
