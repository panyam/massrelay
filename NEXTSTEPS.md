# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.3 released 2026-10-25 (GitHub release, npm `latest`, Go tag), covering #56, #38, #60, #61 and #27's tests.
- No active mission. #66 closed on 2026-11-08 after PR #67 (#13, in-session token refresh) merged; `make exercise-token-refresh` passes at 1f9cc59. Its leftovers are P3: #63, #39, #37, #36. The next mission is a retriage call.
- In-session refresh is unreleased (CHANGELOG `[Unreleased]`); the next release carries it, and mcpkit's exact Go pin needs a bump after.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI. PR #67 fixed the `identity.interop.ts` ordering flake.
- This run: dropped nothing (no threads); feat/bridge-interop-27 merged as PR #65 and its worktree is removed.

## Across threads

- mcpkit's Go side (`experimental/ext/rooms/massrelay/go.mod`) still pins massrelay v0.3.2; bumping it to v0.3.3 is mcpkit work.
- relay01 (relay01.excaliframe.com) didn't answer `/health` on 2026-10-24 or 2026-10-25 (curl timed out); confirm before planning a deploy around it.
