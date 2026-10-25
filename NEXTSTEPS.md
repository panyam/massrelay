# Next steps

Where each line of work left off. Work items live in GitHub issues; this file points at them.
Maintained by /checkpoint: one thread per branch, pruned when the branch merges.

## At a glance

- No open branch threads. main is at v0.3.2, released and published to npm.
- No missions in this repo; they live in servicekit. servicekit#51 (relay connections, streams and evictions on `/metrics` under load) is the one that touches massrelay.
- This run: dropped the v0.2.4 notes. #42, #46, #50 and #16 closed and v0.3.0 to v0.3.2 shipped since.

## Across threads

- #27 (mcpkit bridge interop against a real relay) no longer waits on mcpkit#1581, which is closed. It still needs the relay binary to get a KeyStore; `web/server/app.go` leaves it nil, so the binary can't validate any token.
- relay01 (relay01.excaliframe.com) was down at the last check; confirm before planning a deploy around it.
