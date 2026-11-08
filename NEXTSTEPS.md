# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.5 released 2026-11-08 (GitHub release, npm `latest`, Go proxy): revocation (#73, #63, PR #74) and oneauth v0.3.1 (#37, PR #76). v0.3.4 shipped in-session token refresh the same day.
- No active mission: #72 (`mission_revoke`) closed 2026-11-08. The queue is only P3 (#39, #36) and `waiting` (#75 TokenVerifier adapter, #64, #4, #6). Picking the next mission is a `/retriage` call.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI.
- This run: no threads to prune; PRs #67, #70 and #71 merged, and their worktrees and branches are gone.

## Across threads

- mcpkit's Go side (`experimental/ext/rooms/massrelay/go.mod`) pins massrelay exactly (v0.3.3 as of 2026-11-08). Bumping it to v0.3.5 picks up ReAuth and revocation, and oneauth v0.3.1 with them; that's mcpkit work. Its npm side (`^0.3.x`) gets 0.3.5 on its own.
