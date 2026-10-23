#!/usr/bin/env bash
# massrelay and @panyam/massrelay share one version, the one under
# "## Version" in CAPABILITIES.md. Fails when ts/package.json
# says otherwise, and, given a tag (v0.2.4), when the tag says otherwise too.
# CI runs it on every PR; publish-npm.yml runs it with the tag before publishing.
#
#   scripts/check-versions.sh [tag]
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
want=$(awk '/^## Version/ { getline; print; exit }' "$root/CAPABILITIES.md" | tr -d '[:space:]')
[ -n "$want" ] || { echo "no version under '## Version' in CAPABILITIES.md" >&2; exit 1; }

bad=0
got=$(node -p "require('$root/ts/package.json').version")
if [ "$got" != "$want" ]; then
  echo "ts/package.json is $got; CAPABILITIES.md is $want" >&2
  bad=1
fi
if [ $# -gt 0 ] && [ "${1#v}" != "$want" ]; then
  echo "tag $1 doesn't match CAPABILITIES.md's $want" >&2
  bad=1
fi
[ $bad = 0 ] && echo "all at $want"
exit $bad
