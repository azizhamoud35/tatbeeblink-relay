#!/bin/bash

set -e

echo "🔨 Building simplified relay server..."

# Build the new relay
cd /root/tatbeeb-link/relay
go build -o tatbeeb-link-relay-simple main-simple.go

echo "⏸️  Stopping current relay..."
systemctl stop tatbeeb-link-relay

echo "📦 Backing up current relay..."
cp /opt/tatbeeb-link/tatbeeb-link-relay /opt/tatbeeb-link/tatbeeb-link-relay.backup

echo "🚀 Deploying new relay..."
cp tatbeeb-link-relay-simple /opt/tatbeeb-link/tatbeeb-link-relay

echo "▶️  Starting relay..."
systemctl start tatbeeb-link-relay

echo "✅ Deployment complete!"
echo ""
echo "📊 Status:"
systemctl status tatbeeb-link-relay --no-pager

echo ""
echo "📋 Recent logs:"
journalctl -u tatbeeb-link-relay -n 20 --no-pager

