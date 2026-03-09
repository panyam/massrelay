#!/usr/bin/env bash
#
# Rolling update across all relay hosts.
#
# Usage:
#   ./update-pool.sh                    # update all hosts in inventory.txt
#   ./update-pool.sh r01 r02            # update specific hosts
#   ./update-pool.sh --health           # health check all hosts
#   ./update-pool.sh --status           # show pool status (rooms, peers)
#
# Reads hosts from deploy/inventory.txt (one host per line, # comments ignored).
# Format: <domain> <provider> <region> <ip> <monthly-cost>

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INVENTORY="${SCRIPT_DIR}/../inventory.txt"
DEPLOY_DIR="/opt/massrelay"

# Parse hosts from inventory or command line
get_hosts() {
    if [ $# -gt 0 ] && [[ "$1" != --* ]]; then
        # Hosts from command line args
        echo "$@"
    elif [ -f "$INVENTORY" ]; then
        # Hosts from inventory file
        grep -v '^\s*#' "$INVENTORY" | grep -v '^\s*$' | awk '{print $1}'
    else
        echo "ERROR: No hosts specified and ${INVENTORY} not found." >&2
        echo "Create inventory.txt or pass hosts as arguments." >&2
        exit 1
    fi
}

cmd_update() {
    local hosts
    hosts=$(get_hosts "$@")
    local total
    total=$(echo "$hosts" | wc -w | tr -d ' ')
    local i=0

    echo "==> Updating ${total} relay(s)..."
    echo ""

    for host in $hosts; do
        i=$((i + 1))
        echo "[${i}/${total}] Updating ${host}..."
        ssh "root@${host}" "cd ${DEPLOY_DIR}/deploy/production && git pull && docker compose up -d --build" 2>&1 | sed 's/^/    /'
        # Quick health check after update
        sleep 3
        if curl -sf "https://${host}/health" >/dev/null 2>&1; then
            echo "    ✓ ${host} healthy"
        else
            echo "    ✗ ${host} health check failed!"
        fi
        echo ""
    done

    echo "==> Update complete."
}

cmd_health() {
    local hosts
    hosts=$(get_hosts "$@")

    printf "%-40s %s\n" "HOST" "STATUS"
    printf "%-40s %s\n" "----" "------"

    for host in $hosts; do
        result=$(curl -sf --max-time 5 "https://${host}/health" 2>/dev/null) || result=""
        if [ -n "$result" ]; then
            uptime=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('uptime_seconds',0))" 2>/dev/null || echo "?")
            printf "%-40s ✓ up %ss\n" "$host" "$uptime"
        else
            printf "%-40s ✗ unreachable\n" "$host"
        fi
    done
}

cmd_status() {
    local hosts
    hosts=$(get_hosts "$@")

    printf "%-40s %6s %6s %10s %s\n" "HOST" "ROOMS" "PEERS" "UPTIME" "STATUS"
    printf "%-40s %6s %6s %10s %s\n" "----" "-----" "-----" "------" "------"

    local total_rooms=0
    local total_peers=0

    for host in $hosts; do
        result=$(curl -sf --max-time 5 "https://${host}/health" 2>/dev/null) || result=""
        if [ -n "$result" ]; then
            rooms=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('rooms',0))" 2>/dev/null || echo "0")
            peers=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('peers',0))" 2>/dev/null || echo "0")
            uptime=$(echo "$result" | python3 -c "import sys,json; d=json.load(sys.stdin).get('uptime_seconds',0); print(f'{d//86400}d {(d%86400)//3600}h')" 2>/dev/null || echo "?")
            printf "%-40s %6s %6s %10s ✓\n" "$host" "$rooms" "$peers" "$uptime"
            total_rooms=$((total_rooms + rooms))
            total_peers=$((total_peers + peers))
        else
            printf "%-40s %6s %6s %10s ✗\n" "$host" "-" "-" "-"
        fi
    done

    echo ""
    echo "Total: ${total_rooms} rooms, ${total_peers} peers"
}

# Route to subcommand
case "${1:---help}" in
    --health)
        shift
        cmd_health "$@"
        ;;
    --status)
        shift
        cmd_status "$@"
        ;;
    --help|-h)
        echo "Usage:"
        echo "  $0                     # rolling update all hosts"
        echo "  $0 r01.example.com     # update specific host(s)"
        echo "  $0 --health            # health check all hosts"
        echo "  $0 --status            # pool status (rooms, peers, uptime)"
        echo "  $0 --help              # this help"
        ;;
    *)
        cmd_update "$@"
        ;;
esac
