# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. main is at d7e1abf, unreleased since v0.3.2 (#58, #59, #62, #65 merged; CHANGELOG's Unreleased section is ready for a release, and mcpkit is on `^0.3.2` so it can't use reconnect or `getToken` until one ships).
- No active mission. #53 (deployed auth) closed 2026-10-25 after #65 (#27), `make exercise-auth` passing on d7e1abf; #54 closed 2026-10-24. Next: `retriage` to choose a mission.
- #13 (in-session re-auth) came off `waiting` at P2 now that the relay enforces `exp`; queue.sh flags it as an orphan (P2, no mission) for the next retriage.
- #64 (P3, waiting): `slow-consumer.interop.ts` flaked once under concurrent relay builds; pick up if it fails in CI.
- This run: dropped nothing (no threads); feat/deployed-auth-56, feat/reconnect-38 and feat/token-lifetime-60 are merged.

## Across threads

- relay01 (relay01.excaliframe.com) didn't answer `/health` on 2026-10-24; confirm before planning a deploy around it.
