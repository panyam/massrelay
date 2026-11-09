# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.6 released 2026-11-08 (GitHub release, npm `latest`, Go proxy): oneauth v0.3.2, so Keycloak user tokens report their client (PR #77). v0.3.4 and v0.3.5 shipped token refresh and revocation the same day.
- No active mission, by choice (retriage 2026-11-08): massrelay waits until mcpkit or a deployment turns up the next gap. The queue is P3 (#39 tracing, #36) and `waiting` (#75, #64, #4, #6). If a relay gets hosted again, `make exercise-prod` against its URL is the first check.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI.
- This run: no threads to prune; PRs #67, #70 and #71 merged, and their worktrees and branches are gone.

## Across threads

- None. mcpkit pins massrelay v0.3.6 and oneauth v0.3.2 (checked 2026-11-08).
