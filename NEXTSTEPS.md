# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. main is at 2fb358f, unreleased since v0.3.2 (#58, #59, #62 merged; CHANGELOG's Unreleased section is ready for a release).
- Mission #53 (deployed auth): 2/3, next ready #27 (mcpkit bridge interop against a real relay). `make exercise-auth` passes, last run 2026-10-24 on 2fb358f.
- This run: closed mission #54 (resilient client), exercise passing on 2fb358f.
- #13 (in-session re-auth) came off `waiting` at P2 now that the relay enforces `exp`; queue.sh flags it as an orphan (P2, no mission) for the next retriage.
- This run: dropped nothing (no threads); feat/deployed-auth-56, feat/reconnect-38 and feat/token-lifetime-60 are merged.

## Across threads

- relay01 (relay01.excaliframe.com) didn't answer `/health` on 2026-10-24; confirm before planning a deploy around it.
