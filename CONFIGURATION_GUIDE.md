# Tatbeeb Link Relay - Configuration Guide

## Overview

The Tatbeeb Link Relay server has been configured with production secrets. This guide explains how to deploy and run the relay server.

---

## 🔐 Production Secrets

The following secrets have been generated and configured:

### **1. JWT Secret**
Used to verify JWT tokens from Tatbeeb Link agents.

```
JWT_SECRET=9d94fb6122cc2746706b19b3df4d7bee91cb3563bfa8db204fda38b435a34cff
```

**⚠️ This MUST match the HIS backend configuration!**

### **2. Relay Shared Secret**
Used to authenticate callbacks to HIS backend.

```
RELAY_SHARED_SECRET=a8893933d569fd95c4fbf50619ac32c4d2571719425f83c0c826c0cccd00e15a
```

**⚠️ This MUST match the HIS backend configuration!**

---

## 📋 Configuration File

The production configuration is stored in `config.production.json`:

```json
{
  "server": {
    "controlPort": 8443,
    "tenantPortStart": 50000,
    "tenantPortEnd": 60000
  },
  "jwt": {
    "secret": "9d94fb6122cc2746...",
    "issuer": "his.tatbeeb.sa",
    "audience": "tatbeeb-link.tatbeeb.sa"
  },
  "his": {
    "backendUrl": "https://api-o4z44iwhxa-uc.a.run.app",
    "relaySharedSecret": "a8893933d569fd95...",
    "registerPortEndpoint": "/api/v2/tatbeeb-link/register-port",
    "heartbeatEndpoint": "/api/v2/tatbeeb-link/heartbeat"
  }
}
```

---

## 🚀 Deployment Steps

### **1. Prepare Server**

```bash
# Install on Ubuntu 22.04 LTS
sudo apt update
sudo apt install -y git build-essential

# Install Go 1.21+
wget https://go.dev/dl/go1.21.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

### **2. Clone Repository**

```bash
cd /opt
git clone <repository-url>
cd Tatbeeb/Link
```

### **3. Build Relay**

```bash
cd relay
go build -o tatbeeb-link-relay main.go
```

### **4. Generate TLS Certificates**

```bash
# Option 1: Self-signed certificate (development)
openssl req -x509 -newkey rsa:4096 -nodes \
  -keyout /etc/tatbeeb-link/key.pem \
  -out /etc/tatbeeb-link/cert.pem \
  -days 365 \
  -subj "/CN=relay.tatbeeb.link"

# Option 2: Let's Encrypt (production)
sudo apt install certbot
sudo certbot certonly --standalone -d relay.tatbeeb.link
# Certificates will be in /etc/letsencrypt/live/relay.tatbeeb.link/
```

### **5. Copy Configuration**

```bash
# Copy production config
sudo cp config.production.json /etc/tatbeeb-link/config.json

# Secure the configuration file
sudo chmod 600 /etc/tatbeeb-link/config.json
sudo chown root:root /etc/tatbeeb-link/config.json
```

### **6. Create Systemd Service**

```bash
sudo nano /etc/systemd/system/tatbeeb-link-relay.service
```

```ini
[Unit]
Description=Tatbeeb Link Relay Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/Tatbeeb/Link/relay
ExecStart=/opt/Tatbeeb/Link/relay/tatbeeb-link-relay -config /etc/tatbeeb-link/config.json
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/tatbeeb-link

[Install]
WantedBy=multi-user.target
```

### **7. Configure Firewall**

```bash
# Allow control port (8443)
sudo ufw allow 8443/tcp

# Allow tenant ports (50000-60000)
sudo ufw allow 50000:60000/tcp

# Enable firewall
sudo ufw enable
```

### **8. Start Service**

```bash
# Enable and start service
sudo systemctl daemon-reload
sudo systemctl enable tatbeeb-link-relay
sudo systemctl start tatbeeb-link-relay

# Check status
sudo systemctl status tatbeeb-link-relay

# View logs
sudo journalctl -u tatbeeb-link-relay -f
```

---

## 🧪 Testing

### **1. Test Health Endpoint**

```bash
# From local machine
curl -k https://relay.tatbeeb.link:8443/health

# Expected response
{
  "status": "ok",
  "version": "1.0.0",
  "uptime": "5m30s",
  "activeTenants": 0
}
```

### **2. Test Agent Connection**

```bash
# From agent machine (with test config)
cd /opt/Tatbeeb/Link/agent
./tatbeeb-link-agent -config test-config.json
```

### **3. Verify HIS Callback**

```bash
# Check HIS backend logs
firebase functions:log --only api

# Should see:
# "Port 54321 registered for tenant org_abc123"
```

---

## 📊 Monitoring

### **1. Metrics Endpoint**

Access Prometheus metrics at:
```
http://relay.tatbeeb.link:9090/metrics
```

### **2. Logs**

```bash
# Real-time logs
sudo journalctl -u tatbeeb-link-relay -f

# Recent logs
sudo journalctl -u tatbeeb-link-relay -n 100

# Error logs only
sudo journalctl -u tatbeeb-link-relay -p err
```

### **3. Health Checks**

Set up monitoring with:
- **Uptime Robot**: https://uptimerobot.com
- **Pingdom**: https://www.pingdom.com
- **Grafana + Prometheus**: For advanced metrics

---

## 🔧 Troubleshooting

### **Issue: "Certificate verification failed"**

**Solution:**
```bash
# Regenerate certificate
sudo openssl req -x509 -newkey rsa:4096 -nodes \
  -keyout /etc/tatbeeb-link/key.pem \
  -out /etc/tatbeeb-link/cert.pem \
  -days 365 \
  -subj "/CN=relay.tatbeeb.link"

# Restart service
sudo systemctl restart tatbeeb-link-relay
```

### **Issue: "JWT verification failed"**

**Solution:**
Ensure JWT_SECRET matches HIS backend:
```bash
# Check HIS backend secret
firebase functions:secrets:access TATBEEB_LINK_JWT_SECRET

# Update relay config if needed
sudo nano /etc/tatbeeb-link/config.json
# Update jwt.secret value
sudo systemctl restart tatbeeb-link-relay
```

### **Issue: "Cannot register port with HIS"**

**Solution:**
Verify relay shared secret:
```bash
# Check HIS backend secret
firebase functions:secrets:access TATBEEB_LINK_RELAY_SECRET

# Update relay config
sudo nano /etc/tatbeeb-link/config.json
# Update his.relaySharedSecret value
sudo systemctl restart tatbeeb-link-relay
```

### **Issue: "Port already in use"**

**Solution:**
```bash
# Find process using port 8443
sudo lsof -i :8443

# Kill process
sudo kill -9 <PID>

# Restart service
sudo systemctl restart tatbeeb-link-relay
```

---

## 📈 Performance Tuning

### **1. Connection Limits**

Edit `/etc/tatbeeb-link/config.json`:

```json
{
  "server": {
    "maxConnectionsPerTenant": 20,  // Increase if needed
    "connectionTimeoutSeconds": 600  // Increase for long queries
  }
}
```

### **2. Port Range**

Increase tenant port range:

```json
{
  "server": {
    "tenantPortStart": 50000,
    "tenantPortEnd": 65000  // Support more tenants
  }
}
```

### **3. System Limits**

Increase file descriptor limits:

```bash
# Edit /etc/security/limits.conf
sudo nano /etc/security/limits.conf

# Add:
* soft nofile 65536
* hard nofile 65536

# Reboot
sudo reboot
```

---

## 🔄 Updates

### **Update Relay Binary**

```bash
cd /opt/Tatbeeb/Link/relay
git pull
go build -o tatbeeb-link-relay main.go
sudo systemctl restart tatbeeb-link-relay
```

### **Rotate Secrets**

1. Generate new secrets in HIS:
   ```bash
   firebase functions:secrets:set TATBEEB_LINK_JWT_SECRET
   firebase functions:secrets:set TATBEEB_LINK_RELAY_SECRET
   ```

2. Update relay config:
   ```bash
   sudo nano /etc/tatbeeb-link/config.json
   ```

3. Restart relay:
   ```bash
   sudo systemctl restart tatbeeb-link-relay
   ```

4. Agents will need to re-provision (new JWT will be generated)

---

## 📞 Support

**Logs Location:**
```
/var/log/tatbeeb-link/
```

**Config Location:**
```
/etc/tatbeeb-link/config.json
```

**Binary Location:**
```
/opt/Tatbeeb/Link/relay/tatbeeb-link-relay
```

**Contact:**
- Technical Support: support@tatbeeb.sa
- Emergency: +966-xxx-xxx-xxxx

---

**Last Updated:** October 15, 2025  
**Version:** 1.0.0  
**Status:** Production Ready

