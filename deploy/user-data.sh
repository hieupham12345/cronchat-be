#!/bin/bash
# EC2 cloud-init (Amazon Linux 2023). Runs once on first boot.
# Installs Docker + the Compose v2 plugin and prepares the app directory.
# The actual app deploy (code + .env.prod + compose up) is done over SSH — see README.
set -euxo pipefail

# --- Swap (2 GiB) — important on the 1 GiB t3.micro so `docker build` and the
#     app don't get OOM-killed ---
if [ ! -f /swapfile ]; then
	dd if=/dev/zero of=/swapfile bs=1M count=2048
	chmod 600 /swapfile
	mkswap /swapfile
	swapon /swapfile
	echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

# --- Docker ---
dnf update -y
dnf install -y docker git
systemctl enable --now docker
usermod -aG docker ec2-user

# --- Docker Compose v2 (CLI plugin) ---
COMPOSE_VERSION="v2.32.4"
ARCH="$(uname -m)"   # x86_64 or aarch64
mkdir -p /usr/local/lib/docker/cli-plugins
curl -fsSL "https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-linux-${ARCH}" \
	-o /usr/local/lib/docker/cli-plugins/docker-compose
chmod +x /usr/local/lib/docker/cli-plugins/docker-compose

# --- App directory (code is copied here in a later step) ---
install -d -o ec2-user -g ec2-user /opt/cronchat

echo "cloud-init done: docker $(docker --version), compose $(docker compose version --short 2>/dev/null || echo n/a)"
