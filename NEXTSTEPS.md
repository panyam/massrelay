# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. v0.3.3 released 2026-10-25 (GitHub release, npm `latest`, Go tag), covering #56, #38, #60, #61 and #27's tests.
- Active mission #68 (`mission_prod_stack`), opened 2026-11-08: the three exercises pass through the `make prod-up` stack. relay01 is gone, so this stack stands in for production. Next ready: #69 (P1), which adds `make exercise-prod`.
- #66 closed on 2026-11-08 after PR #67 (#13, in-session token refresh) merged. P3 leftovers: #63 (now only DenySub ending open streams), #37 (the oneauth slice; pushdown filed as panyam/oneauth#456), #39, #36.
- In-session refresh is unreleased (CHANGELOG `[Unreleased]`); the next release carries it, and mcpkit's exact Go pin needs a bump after.
- #64 (waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI. PR #67 fixed the `identity.interop.ts` ordering flake.
- This run: dropped nothing (no threads); feat/bridge-interop-27 merged as PR #65 and its worktree is removed.

## Across threads

- mcpkit's Go side (`experimental/ext/rooms/massrelay/go.mod`) still pins massrelay v0.3.2; bumping it to v0.3.3 is mcpkit work.
