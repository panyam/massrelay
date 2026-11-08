# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.4 released 2026-11-08 (GitHub release, npm `latest`, Go proxy): in-session token refresh (#13, PR #67), `make exercise-prod` (#69, PR #70), and v0.3.3-client compatibility (PR #71).
- Active mission #68 (`mission_prod_stack`): 1/1 tickets closed. `make exercise-prod` passed on main at 65c786c (logged on #68), and a required CI job runs it. Closing it is the user's call. No ticket is ready after it, only P3: #63 (DenySub ending open streams), #37 (the oneauth slice; pushdown is panyam/oneauth#456), #39, #36.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI.
- This run: no threads to prune; feat/reauth-13 (PR #67) and feat/exercise-prod-69 (PR #70) merged, worktrees removed. Their remote branches still exist.

## Across threads

- mcpkit's Go side (`experimental/ext/rooms/massrelay/go.mod`) pins massrelay exactly (v0.3.2 when last checked). Bumping it to v0.3.4 picks up ReAuth; that's mcpkit work. Its npm side (`^0.3.x`) gets 0.3.4 on its own.
