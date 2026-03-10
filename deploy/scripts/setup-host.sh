#!/usr/bin/env bash
#
# Bootstrap a fresh VPS as a massrelay relay server.
#
# Usage:
#   ./setup-host.sh <ip> <domain> [service-name]
#
# Example:
#   ./setup-host.sh 49.12.1.2 r01.relay.excaliframe.com massrelay-r01
#
# What it does:
#   1. Installs Docker (if not present)
#   2. Opens firewall ports 80, 443, 22
#   3. Clones the massrelay repo
#   4. Configures .env with domain and service name
#   5. Starts Caddy + relay via docker compose
#
# Prerequisites:
#   - SSH access to the host (root or user with sudo)
#   - DNS A record for <domain> already pointing to <ip>
#
# Works on: Ubuntu 20.04+, Debian 11+, CentOS/RHEL 8+, Fedora, Amazon Linux 2

set -euo pipefail

IP="${1:?Usage: $0 <ip> <domain> [service-name]}"
DOMAIN="${2:?Usage: $0 <ip> <domain> [service-name]}"
SERVICE_NAME="${3:-massrelay-$(echo "$DOMAIN" | cut -d. -f1)}"
REPO="https://github.com/panyam/massrelay.git"
DEPLOY_DIR="/opt/massrelay"

echo "==> Setting up relay on ${IP}"
echo "    Domain:  ${DOMAIN}"
echo "    Service: ${SERVICE_NAME}"
echo ""

# Verify DNS
RESOLVED_IP=$(dig +short "$DOMAIN" 2>/dev/null | head -1)
if [ "$RESOLVED_IP" != "$IP" ]; then
    echo "WARNING: DNS for ${DOMAIN} resolves to '${RESOLVED_IP}', expected '${IP}'"
    echo "         Caddy won't be able to get a TLS cert until DNS propagates."
    read -p "Continue anyway? [y/N] " -n 1 -r
    echo
    [[ $REPLY =~ ^[Yy]$ ]] || exit 1
fi

ssh "root@${IP}" bash -s <<REMOTE
set -euo pipefail

echo "==> [1/5] Installing Docker..."
if command -v docker &>/dev/null; then
    echo "    Docker already installed: \$(docker --version)"
else
    curl -fsSL https://get.docker.com | sh
    systemctl enable docker
    systemctl start docker
    echo "    Installed: \$(docker --version)"
fi

echo "==> [2/5] Configuring firewall..."
if command -v ufw &>/dev/null; then
    ufw allow 22/tcp
    ufw allow 80/tcp
    ufw allow 443/tcp
    ufw --force enable
    echo "    ufw configured"
elif command -v firewall-cmd &>/dev/null; then
    firewall-cmd --permanent --add-service=ssh
    firewall-cmd --permanent --add-service=http
    firewall-cmd --permanent --add-service=https
    firewall-cmd --reload
    echo "    firewalld configured"
else
    echo "    No firewall manager found (ufw/firewalld). Ensure ports 80, 443, 22 are open."
fi

echo "==> [3/5] Cloning massrelay..."
if [ -d "${DEPLOY_DIR}" ]; then
    cd "${DEPLOY_DIR}" && git pull
else
    git clone "${REPO}" "${DEPLOY_DIR}"
fi

echo "==> [4/5] Configuring .env..."
cd "${DEPLOY_DIR}/deploy/production"
cp -n .env.example .env 2>/dev/null || true
sed -i "s|^RELAY_DOMAIN=.*|RELAY_DOMAIN=${DOMAIN}|" .env
sed -i "s|^OTEL_SERVICE_NAME=.*|OTEL_SERVICE_NAME=${SERVICE_NAME}|" .env
echo "    .env configured:"
grep -E "^(RELAY_DOMAIN|OTEL_SERVICE_NAME)=" .env

echo "==> [5/5] Starting services..."
docker compose up -d --build
echo ""
echo "==> Relay is starting. Caddy will obtain TLS cert automatically."
echo "    Verify: curl https://${DOMAIN}/health"
REMOTE

echo ""
echo "==> Done! Relay deployed at https://${DOMAIN}"
echo ""
echo "Next steps:"
echo "  1. Verify:  curl https://${DOMAIN}/health"
echo "  2. Add to relay-pool.json:"
echo "     {\"url\": \"wss://${DOMAIN}\", \"region\": \"<your-region>\"}"
