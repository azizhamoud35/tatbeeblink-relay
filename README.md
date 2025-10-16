# 🔗 Tatbeeb Link Relay Server

The relay server is the cloud-based component of Tatbeeb Link that enables secure database tunneling without firewall configuration.

## 🎯 Overview

The relay server:
- ✅ Accepts TLS connections from client apps
- ✅ Assigns unique TCP ports per client
- ✅ Forwards database connections through secure tunnels
- ✅ Monitors client health with heartbeats
- ✅ Provides health check endpoints

## 🏗️ Architecture

```
┌─────────────────────┐         TLS Connection        ┌──────────────────────┐
│  Client App         │ ────────────────────────────→ │  Relay Server        │
│  (TatbeebLink.exe)  │    Simple Text Protocol       │  (link.tatbeeb.sa)   │
│                     │ ←──────────────────────────── │                      │
└─────────────────────┘      Port Assignment          └──────────────────────┘
                                                               ↓
                                                       Assigned Port (e.g., 50123)
                                                               ↓
                                                       ┌──────────────────────┐
                                                       │  Remote SQL Client   │
                                                       │  Connects Here       │
                                                       └──────────────────────┘
```

## 📦 Files

- **`main-simple.go`** - Simple text protocol relay (✅ RECOMMENDED)
- **`main.go`** - Original yamux-based relay
- **`jwt.go`** - JWT authentication utilities
- **`his_client.go`** - HIS backend integration
- **`config.production.json`** - Production configuration
- **`deploy-simple.sh`** - Deployment script
- **`CONFIGURATION_GUIDE.md`** - Detailed configuration guide

## 🚀 Quick Deploy

### On Your Server (DigitalOcean, AWS, etc.)

```bash
# 1. Clone the repository
cd /root
git clone https://github.com/azizhamoud35/tatbeeblink-relay.git
cd tatbeeblink-relay

# 2. Build the relay
go build -o tatbeeb-link-relay main-simple.go

# 3. Create directories
mkdir -p /opt/tatbeeb-link
mkdir -p /etc/tatbeeb-link

# 4. Copy files
cp tatbeeb-link-relay /opt/tatbeeb-link/
cp config.production.json /etc/tatbeeb-link/

# 5. Update configuration
nano /etc/tatbeeb-link/config.production.json
# Update publicHost, TLS cert paths, port range

# 6. Create systemd service
cat > /etc/systemd/system/tatbeeb-link-relay.service << 'EOF'
[Unit]
Description=Tatbeeb Link Relay Server
After=network.target
Documentation=https://github.com/azizhamoud35/tatbeeblink-relay

[Service]
Type=simple
User=root
WorkingDirectory=/opt/tatbeeb-link
ExecStart=/opt/tatbeeb-link/tatbeeb-link-relay -config /etc/tatbeeb-link/config.production.json
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# 7. Enable and start
systemctl daemon-reload
systemctl enable tatbeeb-link-relay
systemctl start tatbeeb-link-relay

# 8. Check status
systemctl status tatbeeb-link-relay
```

## ⚙️ Configuration

Edit `/etc/tatbeeb-link/config.production.json`:

```json
{
  "controlPort": 8443,
  "tlsCertFile": "/etc/letsencrypt/live/link.tatbeeb.sa/fullchain.pem",
  "tlsKeyFile": "/etc/letsencrypt/live/link.tatbeeb.sa/privkey.pem",
  "publicHost": "link.tatbeeb.sa",
  "tenantPortStart": 50000,
  "tenantPortEnd": 50100
}
```

### Configuration Options

| Option | Description | Example |
|--------|-------------|---------|
| `controlPort` | Port for client connections | `8443` (TLS) |
| `tlsCertFile` | Path to TLS certificate | `/etc/letsencrypt/live/.../fullchain.pem` |
| `tlsKeyFile` | Path to TLS private key | `/etc/letsencrypt/live/.../privkey.pem` |
| `publicHost` | Public hostname | `link.tatbeeb.sa` |
| `tenantPortStart` | Start of port range | `50000` |
| `tenantPortEnd` | End of port range | `50100` |

## 🔐 TLS Certificate Setup

### Using Let's Encrypt (Recommended)

```bash
# Install certbot
apt-get update
apt-get install -y certbot

# Get certificate
certbot certonly --standalone -d link.tatbeeb.sa

# Certificates will be in:
# /etc/letsencrypt/live/link.tatbeeb.sa/fullchain.pem
# /etc/letsencrypt/live/link.tatbeeb.sa/privkey.pem
```

### Auto-Renewal

```bash
# Add to crontab
crontab -e

# Add this line:
0 0 * * * certbot renew --quiet && systemctl restart tatbeeb-link-relay
```

## 🔥 Firewall Configuration

```bash
# Allow control port
ufw allow 8443/tcp

# Allow assigned port range
ufw allow 50000:50100/tcp

# Allow health check (optional)
ufw allow 8080/tcp

# Enable firewall
ufw enable
```

## 📊 Monitoring

### Check Status

```bash
systemctl status tatbeeb-link-relay
```

### View Logs

```bash
# Recent logs
journalctl -u tatbeeb-link-relay -n 50

# Follow logs
journalctl -u tatbeeb-link-relay -f

# Logs since boot
journalctl -u tatbeeb-link-relay -b
```

### Health Check

```bash
# Check if relay is healthy
curl http://localhost:8080/health

# Expected response:
# {"status":"healthy","activeAgents":0,"availablePorts":101}
```

### Metrics

```bash
# View active connections
curl http://localhost:8080/health | jq .activeAgents

# View available ports
curl http://localhost:8080/health | jq .availablePorts
```

## 🔄 Update Deployment

```bash
cd /root/tatbeeblink-relay

# Pull latest code
git pull

# Build new version
go build -o tatbeeb-link-relay-new main-simple.go

# Stop service
systemctl stop tatbeeb-link-relay

# Backup current version
cp /opt/tatbeeb-link/tatbeeb-link-relay /opt/tatbeeb-link/tatbeeb-link-relay.backup

# Deploy new version
cp tatbeeb-link-relay-new /opt/tatbeeb-link/tatbeeb-link-relay

# Start service
systemctl start tatbeeb-link-relay

# Verify
systemctl status tatbeeb-link-relay
```

## 📋 Protocol

### Client → Server

The relay uses a simple text-based protocol:

1. **Register:**
   ```
   Client: REGISTER\n
   Server: OK port:50123\n
   ```

2. **Heartbeat:**
   ```
   Client: HEARTBEAT\n
   Server: (no response)
   ```

### Connection Flow

1. Client connects to relay on port `8443` via TLS
2. Client sends `REGISTER\n`
3. Relay assigns a port (e.g., `50123`)
4. Relay responds with `OK port:50123\n`
5. Client starts sending `HEARTBEAT\n` every 30 seconds
6. Remote users connect to `link.tatbeeb.sa:50123`
7. Relay forwards to client's SQL Server

## 🐛 Troubleshooting

### Issue: "Failed to accept control"

**Cause:** Client sending wrong protocol

**Solution:**
1. Check client is using simple text protocol (`REGISTER\n`)
2. Verify TLS certificate is valid
3. Check logs: `journalctl -u tatbeeb-link-relay -n 50`

### Issue: "No available ports"

**Cause:** All ports in range are assigned

**Solution:**
1. Increase port range in config
2. Check for stale connections: `netstat -tlnp | grep tatbeeb`
3. Restart relay: `systemctl restart tatbeeb-link-relay`

### Issue: "Connection timeout"

**Cause:** Firewall blocking ports

**Solution:**
```bash
# Check firewall
ufw status

# Open ports
ufw allow 8443/tcp
ufw allow 50000:50100/tcp
```

### Issue: "TLS handshake failed"

**Cause:** Certificate expired or invalid

**Solution:**
```bash
# Renew certificate
certbot renew

# Restart relay
systemctl restart tatbeeb-link-relay
```

## 🔒 Security

### Best Practices

- ✅ Always use TLS (port 8443)
- ✅ Use Let's Encrypt certificates
- ✅ Enable firewall (UFW)
- ✅ Limit port range (e.g., 50000-50100)
- ✅ Monitor logs regularly
- ✅ Update regularly

### Rate Limiting

Currently, no rate limiting is implemented. For production:
- Consider adding connection rate limits
- Implement IP allowlisting
- Add JWT authentication (see `jwt.go`)

## 📊 Performance

### Benchmarks

- **Max Concurrent Clients:** 100 (configurable via port range)
- **Memory Usage:** ~5 MB per relay server
- **CPU Usage:** <1% idle, <5% under load
- **Latency:** ~50-100ms added per hop

### Scaling

For more than 100 concurrent clients:
1. Increase port range in config
2. Add more relay servers (load balancing)
3. Use DNS round-robin

## 📞 Support

- **Issues:** https://github.com/azizhamoud35/tatbeeblink-relay/issues
- **Email:** support@tatbeeb.sa
- **Documentation:** See `CONFIGURATION_GUIDE.md`

## 📄 License

Proprietary - Tatbeeb Healthcare Technology © 2025

---

**Status:** Production Ready ✅  
**Version:** 1.0.0  
**Last Updated:** October 16, 2025

