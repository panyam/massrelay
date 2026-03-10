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
#   1. Copies your SSH key (if needed)
#   2. Hardens SSH (disables password auth)
#   3. Installs prerequisites (git, firewall)
#   4. Installs Docker (if not present)
#   5. Configures firewall (ports 80, 443, 22)
#   6. Clones the massrelay repo
#   7. Configures .env and starts Caddy + relay
#
# Prerequisites:
#   - SSH access to the host (root, initial password or key)
#   - DNS A record for <domain> already pointing to <ip>
#
# SSH key:
#   Uses ~/.ssh/id_ed25519.pub by default. Override with SSH_KEY env var:
#     SSH_KEY=~/.ssh/id_custom.pub ./setup-host.sh ...
#
# Works on: Ubuntu 20.04+, Debian 11+, CentOS/RHEL 8+, Fedora, AlmaLinux 9+

set -euo pipefail

IP="${1:?Usage: $0 <ip> <domain> [service-name]}"
DOMAIN="${2:?Usage: $0 <ip> <domain> [service-name]}"
SERVICE_NAME="${3:-massrelay-$(echo "$DOMAIN" | cut -d. -f1)}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519.pub}"
REPO="https://github.com/panyam/massrelay.git"
DEPLOY_DIR="/opt/massrelay"

echo "==> Setting up relay on ${IP}"
echo "    Domain:  ${DOMAIN}"
echo "    Service: ${SERVICE_NAME}"
echo "    SSH key: ${SSH_KEY}"
echo ""

# Verify SSH key exists
if [ ! -f "$SSH_KEY" ]; then
    echo "ERROR: SSH public key not found: ${SSH_KEY}"
    echo "       Set SSH_KEY env var or generate one: ssh-keygen -t ed25519"
    exit 1
fi

# Verify DNS
RESOLVED_IP=$(dig +short "$DOMAIN" 2>/dev/null | head -1)
if [ "$RESOLVED_IP" != "$IP" ]; then
    echo "WARNING: DNS for ${DOMAIN} resolves to '${RESOLVED_IP}', expected '${IP}'"
    echo "         Caddy won't be able to get a TLS cert until DNS propagates."
    read -p "Continue anyway? [y/N] " -n 1 -r
    echo
    [[ $REPLY =~ ^[Yy]$ ]] || exit 1
fi

# Step 1: SSH key setup (runs before hardening, may need password auth)
echo "==> [1/7] Setting up SSH key access..."
if ssh -o BatchMode=yes -o ConnectTimeout=5 "root@${IP}" true 2>/dev/null; then
    echo "    SSH key already works, skipping ssh-copy-id"
else
    echo "    Copying SSH key (will prompt for password)..."
    ssh-copy-id -i "$SSH_KEY" "root@${IP}"
fi

# Verify key auth works before hardening
if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "root@${IP}" true 2>/dev/null; then
    echo "ERROR: SSH key auth failed. Cannot proceed with hardening."
    exit 1
fi

# Steps 2-6: Run on remote host
ssh -o BatchMode=yes "root@${IP}" bash -s <<REMOTE
set -euo pipefail

echo "==> [2/7] Hardening SSH..."
SSHD_CONFIG="/etc/ssh/sshd_config"

# Disable password auth, root password login, and empty passwords
# Use a drop-in config to avoid clobbering vendor defaults
mkdir -p /etc/ssh/sshd_config.d
cat > /etc/ssh/sshd_config.d/99-hardening.conf <<'SSHEOF'
# Massrelay SSH hardening — managed by setup-host.sh
PasswordAuthentication no
PermitRootLogin prohibit-password
PermitEmptyPasswords no
ChallengeResponseAuthentication no
UsePAM yes
SSHEOF

# Restart sshd (works on both systemd service names)
if systemctl is-active sshd &>/dev/null; then
    systemctl restart sshd
elif systemctl is-active ssh &>/dev/null; then
    systemctl restart ssh
fi
echo "    SSH hardened: password auth disabled, key-only root login"

echo "==> [3/7] Installing prerequisites..."
. /etc/os-release
if [ "\$ID" = "almalinux" ] || [ "\$ID" = "rocky" ] || [ "\$ID" = "centos" ]; then
    dnf clean packages 2>/dev/null || true
    dnf -y install git firewalld dnf-plugins-core
elif [ "\$ID" = "ubuntu" ] || [ "\$ID" = "debian" ]; then
    apt-get update -qq && apt-get install -y -qq git ufw
fi

echo "==> [4/7] Installing Docker..."
if command -v docker &>/dev/null; then
    echo "    Docker already installed: \$(docker --version)"
else
    if [ "\$ID" = "almalinux" ] || [ "\$ID" = "rocky" ]; then
        echo "    Detected \${ID} — using Docker CentOS repo"
        dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        dnf -y install docker-ce docker-ce-cli containerd.io docker-compose-plugin
    else
        curl -fsSL https://get.docker.com | sh
    fi
    systemctl enable docker
    systemctl start docker
    echo "    Installed: \$(docker --version)"
fi

echo "==> [5/7] Configuring firewall..."
if command -v ufw &>/dev/null; then
    ufw allow 22/tcp
    ufw allow 80/tcp
    ufw allow 443/tcp
    ufw --force enable
    echo "    ufw configured"
elif command -v firewall-cmd &>/dev/null; then
    systemctl enable firewalld
    systemctl start firewalld
    firewall-cmd --permanent --add-service=ssh
    firewall-cmd --permanent --add-service=http
    firewall-cmd --permanent --add-service=https
    firewall-cmd --reload
    echo "    firewalld configured"
else
    echo "    No firewall manager found (ufw/firewalld). Ensure ports 80, 443, 22 are open."
fi

if [ -d "${DEPLOY_DIR}" ]; then
    cd "${DEPLOY_DIR}" && git pull
else
    git clone "${REPO}" "${DEPLOY_DIR}"
fi

echo "==> [7/7] Configuring and starting relay..."
cd "${DEPLOY_DIR}/deploy/production"
cp -n .env.example .env 2>/dev/null || true
sed -i "s|^RELAY_DOMAIN=.*|RELAY_DOMAIN=${DOMAIN}|" .env
sed -i "s|^OTEL_SERVICE_NAME=.*|OTEL_SERVICE_NAME=${SERVICE_NAME}|" .env
echo "    .env configured:"
grep -E "^(RELAY_DOMAIN|OTEL_SERVICE_NAME)=" .env

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
echo ""
echo "SSH hardening applied:"
echo "  - Password authentication: disabled"
echo "  - Root login: key-only (prohibit-password)"
echo "  - Config: /etc/ssh/sshd_config.d/99-hardening.conf"
echo ""
echo "To add/rotate SSH keys later:"
echo "  ssh root@${IP} \"echo 'ssh-ed25519 AAAA...' >> ~/.ssh/authorized_keys\""
