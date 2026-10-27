# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.3 released 2026-10-25 (GitHub release, npm `latest`, Go tag), covering #56, #38, #60, #61 and #27's tests.
- Active mission #66 (`mission_token_refresh`), opened 2026-10-26: a client stays in its room past its token's `exp`. Next ready: #13 (P1). Its PR adds `make exercise-token-refresh` as its first commit (failing), then ReAuth on the relay and in `CollabClient`. #63 stays P3, outside the mission.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI.
- This run: dropped nothing (no threads); feat/bridge-interop-27 merged as PR #65 and its worktree is removed.

## Across threads

- mcpkit's Go side (`experimental/ext/rooms/massrelay/go.mod`) still pins massrelay v0.3.2; bumping it to v0.3.3 is mcpkit work.
- relay01 (relay01.excaliframe.com) didn't answer `/health` on 2026-10-24 or 2026-10-25 (curl timed out); confirm before planning a deploy around it.
