# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.3 is the latest release (2026-10-25). `main` adds in-session token refresh (#13, PR #67) and `make exercise-prod` (#69, PR #70), both under CHANGELOG `[Unreleased]`. A release is next.
- Active mission #68 (`mission_prod_stack`): 1/1 tickets closed. `make exercise-prod` passed on main at 65c786c (logged on #68), and a required CI job runs it. Closing it is the user's call. No ticket is ready after it, only P3: #63 (DenySub ending open streams), #37 (the oneauth slice; pushdown is panyam/oneauth#456), #39, #36.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI.
- This run: no threads to prune; feat/reauth-13 (PR #67) and feat/exercise-prod-69 (PR #70) merged, worktrees removed. Their remote branches still exist.

## Across threads

- mcpkit's Go side (`experimental/ext/rooms/massrelay/go.mod`) pins massrelay exactly (v0.3.2 when last checked). After the next release it needs a bump in mcpkit to pick up ReAuth.
