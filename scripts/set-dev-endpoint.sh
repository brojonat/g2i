#!/bin/bash
# Auto-detect the public endpoint for Minio in dev

# Try Tailscale hostname first (best for cross-machine access)
if command -v tailscale &> /dev/null; then
    TAILSCALE_HOST=$(tailscale status --json 2>/dev/null | jq -r '.Self.DNSName' | sed 's/\.$//')
    if [ -n "$TAILSCALE_HOST" ] && [ "$TAILSCALE_HOST" != "null" ]; then
        echo "${TAILSCALE_HOST}:9000"
        exit 0
    fi

    # Try Tailscale IP
    TAILSCALE_IP=$(tailscale ip -4 2>/dev/null)
    if [ -n "$TAILSCALE_IP" ]; then
        echo "${TAILSCALE_IP}:9000"
        exit 0
    fi
fi

# Fall back to primary non-localhost IP
PRIMARY_IP=$(ip addr show | grep "inet " | grep -v "127.0.0.1" | head -1 | awk '{print $2}' | cut -d'/' -f1)
if [ -n "$PRIMARY_IP" ]; then
    echo "${PRIMARY_IP}:9000"
    exit 0
fi

# Last resort: localhost
echo "localhost:9000"
