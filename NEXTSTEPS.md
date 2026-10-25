# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.3 released 2026-10-25 (GitHub release, npm `latest`, Go tag), covering #56, #38, #60, #61 and #27's tests.
- No active mission: #53 (deployed auth) closed 2026-10-25 with `make exercise-auth` passing on d7e1abf, #54 closed 2026-10-24. Next: `retriage` to choose one; #13 (in-session re-auth, P2 orphan) is the likely seed.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI.
- This run: dropped nothing (no threads); feat/bridge-interop-27 merged as PR #65 and its worktree is removed.

## Across threads

- mcpkit's Go side (`experimental/ext/rooms/massrelay/go.mod`) still pins massrelay v0.3.2; bumping it to v0.3.3 is mcpkit work.
- relay01 (relay01.excaliframe.com) didn't answer `/health` on 2026-10-24 or 2026-10-25 (curl timed out); confirm before planning a deploy around it.
