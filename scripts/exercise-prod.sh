#!/usr/bin/env bash
# Mission #68's exercise: the three mission exercises (auth, reconnect, token
# refresh) through the `make prod-up` stack: Caddy with TLS in front of the
# relay container, auth loaded from env, and the stack's own rate limits.
#
# Replaces any running prod-up stack, and takes it down when done.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
compose="docker compose -f $root/deploy/production/docker-compose.yml"
keys=deploy/production/secrets/exercise-keys.json
work=$(mktemp -d)

cleanup() {
  $compose down >/dev/null 2>&1 || true
  rm -f "$keys"
  rm -rf "$work"
}
trap cleanup EXIT

# The key testKey('exercise-app', 'exercise-issuer') in ts/interop/jwt.ts signs
# with. A separate file name, so a real keys.json is never overwritten.
printf '[{"client_id":"exercise-app","alg":"HS256","key":"exercise-app-secret-at-least-32-bytes-long"}]\n' > "$keys"

RELAY_AUTH_REQUIRED=true \
RELAY_AUTH_KEYS_FILE=/etc/massrelay/exercise-keys.json \
RELAY_AUTH_ISSUER=exercise-issuer \
  make prod-up

# Caddy issues localhost a certificate from its own local CA. Node trusts it
# through NODE_EXTRA_CA_CERTS.
ca=$work/caddy-root.crt
for _ in $(seq 1 60); do
  $compose cp caddy:/data/caddy/pki/authorities/local/root.crt "$ca" >/dev/null 2>&1 && break
  sleep 1
done
[ -s "$ca" ] || { echo "Caddy never wrote its local root CA" >&2; exit 1; }
for _ in $(seq 1 60); do
  curl -fsS --cacert "$ca" https://localhost/health >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS --cacert "$ca" https://localhost/health >/dev/null

cd ts
NODE_EXTRA_CA_CERTS=$ca \
MASSRELAY_RELAY_URL=wss://localhost \
MASSRELAY_RELAY_RESTART="$compose restart relay" \
  npm run exercise
