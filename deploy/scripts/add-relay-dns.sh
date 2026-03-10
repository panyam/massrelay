#!/usr/bin/env bash
set -euo pipefail

# Add a relay subdomain A record to excaliframe.com via Namecheap API.
#
# Usage:
#   ./add-relay-dns.sh <subdomain> <ip>
#   ./add-relay-dns.sh relay01 85.215.x.x
#
# Requires env vars: NAMECHEAP_API_USER, NAMECHEAP_API_KEY, NAMECHEAP_CLIENT_IP

SUBDOMAIN="${1:?Usage: $0 <subdomain> <ip>}"
IP="${2:?Usage: $0 <subdomain> <ip>}"
SLD="excaliframe"
TLD="com"

# Verify env vars
: "${NAMECHEAP_API_USER:?Set NAMECHEAP_API_USER}"
: "${NAMECHEAP_API_KEY:?Set NAMECHEAP_API_KEY}"
: "${NAMECHEAP_CLIENT_IP:?Set NAMECHEAP_CLIENT_IP (your whitelisted IP)}"

echo "==> Fetching existing DNS records for ${SLD}.${TLD}..."

# Fetch current records
RESPONSE=$(curl -s -G "https://api.namecheap.com/xml.response" \
  --data-urlencode "ApiUser=${NAMECHEAP_API_USER}" \
  --data-urlencode "ApiKey=${NAMECHEAP_API_KEY}" \
  --data-urlencode "UserName=${NAMECHEAP_API_USER}" \
  --data-urlencode "ClientIp=${NAMECHEAP_CLIENT_IP}" \
  --data-urlencode "Command=namecheap.domains.dns.getHosts" \
  --data-urlencode "SLD=${SLD}" \
  --data-urlencode "TLD=${TLD}")

# Check for API errors
if echo "$RESPONSE" | grep -q 'Status="ERROR"'; then
  echo "ERROR: Namecheap API error:"
  echo "$RESPONSE" | grep -o '<Error[^>]*>[^<]*</Error>'
  exit 1
fi

# Parse existing records into setHosts parameters
# Namecheap setHosts requires ALL records to be sent (it replaces everything)
PARAMS=()
INDEX=1

while IFS= read -r line; do
  NAME=$(echo "$line" | grep -o 'Name="[^"]*"' | cut -d'"' -f2)
  TYPE=$(echo "$line" | grep -o 'Type="[^"]*"' | cut -d'"' -f2)
  ADDR=$(echo "$line" | grep -o 'Address="[^"]*"' | cut -d'"' -f2)
  TTL=$(echo "$line" | grep -o 'TTL="[^"]*"' | cut -d'"' -f2)
  MXPREF=$(echo "$line" | grep -o 'MXPref="[^"]*"' | cut -d'"' -f2)

  PARAMS+=("HostName${INDEX}=${NAME}")
  PARAMS+=("RecordType${INDEX}=${TYPE}")
  PARAMS+=("Address${INDEX}=${ADDR}")
  PARAMS+=("TTL${INDEX}=${TTL}")
  if [ "$TYPE" = "MX" ]; then
    PARAMS+=("MXPref${INDEX}=${MXPREF}")
  fi
  INDEX=$((INDEX + 1))
done < <(echo "$RESPONSE" | grep '<host ')

EXISTING_COUNT=$((INDEX - 1))
echo "    Found ${EXISTING_COUNT} existing records"

# Check if subdomain already exists
if echo "$RESPONSE" | grep -q "Name=\"${SUBDOMAIN}\""; then
  echo "WARNING: Record for '${SUBDOMAIN}' already exists. It will be updated."
  # Remove the old record from params (rebuild without it)
  PARAMS=()
  INDEX=1
  while IFS= read -r line; do
    NAME=$(echo "$line" | grep -o 'Name="[^"]*"' | cut -d'"' -f2)
    TYPE=$(echo "$line" | grep -o 'Type="[^"]*"' | cut -d'"' -f2)
    ADDR=$(echo "$line" | grep -o 'Address="[^"]*"' | cut -d'"' -f2)
    TTL=$(echo "$line" | grep -o 'TTL="[^"]*"' | cut -d'"' -f2)
    MXPREF=$(echo "$line" | grep -o 'MXPref="[^"]*"' | cut -d'"' -f2)

    # Skip the existing record for this subdomain+A
    if [ "$NAME" = "$SUBDOMAIN" ] && [ "$TYPE" = "A" ]; then
      continue
    fi

    PARAMS+=("HostName${INDEX}=${NAME}")
    PARAMS+=("RecordType${INDEX}=${TYPE}")
    PARAMS+=("Address${INDEX}=${ADDR}")
    PARAMS+=("TTL${INDEX}=${TTL}")
    if [ "$TYPE" = "MX" ]; then
      PARAMS+=("MXPref${INDEX}=${MXPREF}")
    fi
    INDEX=$((INDEX + 1))
  done < <(echo "$RESPONSE" | grep '<host ')
fi

# Add the new relay record
PARAMS+=("HostName${INDEX}=${SUBDOMAIN}")
PARAMS+=("RecordType${INDEX}=A")
PARAMS+=("Address${INDEX}=${IP}")
PARAMS+=("TTL${INDEX}=1799")

TOTAL=$((INDEX))
echo "==> Setting ${TOTAL} records (${EXISTING_COUNT} existing + relay01 A → ${IP})..."

# Build the curl command
CURL_ARGS=()
CURL_ARGS+=(--data-urlencode "ApiUser=${NAMECHEAP_API_USER}")
CURL_ARGS+=(--data-urlencode "ApiKey=${NAMECHEAP_API_KEY}")
CURL_ARGS+=(--data-urlencode "UserName=${NAMECHEAP_API_USER}")
CURL_ARGS+=(--data-urlencode "ClientIp=${NAMECHEAP_CLIENT_IP}")
CURL_ARGS+=(--data-urlencode "Command=namecheap.domains.dns.setHosts")
CURL_ARGS+=(--data-urlencode "SLD=${SLD}")
CURL_ARGS+=(--data-urlencode "TLD=${TLD}")

for PARAM in "${PARAMS[@]}"; do
  CURL_ARGS+=(--data-urlencode "$PARAM")
done

SET_RESPONSE=$(curl -s -G "https://api.namecheap.com/xml.response" "${CURL_ARGS[@]}")

if echo "$SET_RESPONSE" | grep -q 'Status="OK"'; then
  echo "==> DNS record created: ${SUBDOMAIN}.${SLD}.${TLD} → ${IP}"
  echo ""
  echo "    Verify propagation (may take a few minutes):"
  echo "    dig +short ${SUBDOMAIN}.${SLD}.${TLD}"
else
  echo "ERROR: Failed to set DNS records:"
  echo "$SET_RESPONSE" | grep -o '<Error[^>]*>[^<]*</Error>'
  exit 1
fi
