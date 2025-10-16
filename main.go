package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/tatbeeb/tatbeeb-link/common"
)

type Tenant struct {
	ID             string
	AssignedPort   int
	SQLUser        string
	SQLPassword    string
	ControlSession *yamux.Session
	Listener       net.Listener
	ActiveConns    int
	mu             sync.Mutex
}

type RelayServer struct {
	config        *common.RelayConfig
	tenants       map[string]*Tenant
	portPool      []int
	nextPortIndex int
	mu            sync.RWMutex
	hisClient     *HISClient
	jwtSecret     string
	jwtIssuer     string
	jwtAudience   string
}

func NewRelayServer(config *common.RelayConfig, hisBackendURL, relaySecret, jwtSecret string) *RelayServer {
	// Initialize port pool
	portPool := make([]int, 0, config.TenantPortEnd-config.TenantPortStart+1)
	for p := config.TenantPortStart; p <= config.TenantPortEnd; p++ {
		portPool = append(portPool, p)
	}

	// Initialize HIS client
	hisClient := NewHISClient(hisBackendURL, relaySecret)

	return &RelayServer{
		config:      config,
		tenants:     make(map[string]*Tenant),
		portPool:    portPool,
		hisClient:   hisClient,
		jwtSecret:   jwtSecret,
		jwtIssuer:   "his.tatbeeb.sa",
		jwtAudience: "tatbeeb-link.tatbeeb.sa",
	}
}

func (s *RelayServer) Start() error {
	// Start health check HTTP server
	go s.startHealthCheckServer()

	// Load TLS certificate
	cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Start control listener
	listener, err := tls.Listen("tcp", fmt.Sprintf(":%d", s.config.ControlPort), tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to start control listener: %w", err)
	}

	log.Printf("🚀 Tatbeeb Link Relay started")
	log.Printf("   Control port: %d (TLS)", s.config.ControlPort)
	log.Printf("   Tenant ports: %d-%d", s.config.TenantPortStart, s.config.TenantPortEnd)
	log.Printf("   Health check: http://localhost:9090/health")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		go s.handleControlConnection(conn)
	}
}

func (s *RelayServer) startHealthCheckServer() {
	http.HandleFunc("/health", s.handleHealth)
	http.HandleFunc("/metrics", s.handleMetrics)

	log.Printf("Health check server listening on :9090")
	if err := http.ListenAndServe(":9090", nil); err != nil {
		log.Printf("Health check server error: %v", err)
	}
}

func (s *RelayServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	activeTenants := len(s.tenants)
	s.mu.RUnlock()

	health := map[string]interface{}{
		"status":        "ok",
		"version":       "1.0.0",
		"activeTenants": activeTenants,
		"timestamp":     time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func (s *RelayServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	metrics := map[string]interface{}{
		"active_tenants":    len(s.tenants),
		"available_ports":   len(s.portPool) - s.nextPortIndex,
		"total_connections": s.getTotalConnections(),
		"tenants":           s.getTenantMetrics(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func (s *RelayServer) getTotalConnections() int {
	total := 0
	for _, tenant := range s.tenants {
		tenant.mu.Lock()
		total += tenant.ActiveConns
		tenant.mu.Unlock()
	}
	return total
}

func (s *RelayServer) getTenantMetrics() []map[string]interface{} {
	metrics := make([]map[string]interface{}, 0, len(s.tenants))
	for _, tenant := range s.tenants {
		tenant.mu.Lock()
		metrics = append(metrics, map[string]interface{}{
			"tenantId":     tenant.ID,
			"assignedPort": tenant.AssignedPort,
			"activeConns":  tenant.ActiveConns,
		})
		tenant.mu.Unlock()
	}
	return metrics
}

func (s *RelayServer) handleControlConnection(conn net.Conn) {
	defer conn.Close()

	// Create yamux session (server mode)
	session, err := yamux.Server(conn, nil)
	if err != nil {
		log.Printf("Failed to create yamux session: %v", err)
		return
	}
	defer session.Close()

	// Accept control stream
	stream, err := session.AcceptStream()
	if err != nil {
		log.Printf("Failed to accept control stream: %v", err)
		return
	}
	defer stream.Close()

	// Read registration message
	var buf [4096]byte
	n, err := stream.Read(buf[:])
	if err != nil {
		log.Printf("Failed to read registration: %v", err)
		return
	}

	msg, err := common.DecodeMessage(buf[:n])
	if err != nil {
		log.Printf("Failed to decode message: %v", err)
		return
	}

	if msg.Type != common.MsgTypeRegister {
		log.Printf("Expected register message, got: %s", msg.Type)
		return
	}

	var regPayload common.RegisterPayload
	if err := common.DecodePayload(msg, &regPayload); err != nil {
		log.Printf("Failed to decode registration payload: %v", err)
		return
	}

	// Verify JWT token
	claims, err := VerifyJWT(regPayload.JWT, s.jwtSecret, s.jwtIssuer, s.jwtAudience)
	if err != nil {
		log.Printf("JWT verification failed for tenant %s: %v", regPayload.TenantID, err)
		s.sendError(stream, "INVALID_JWT", fmt.Sprintf("JWT verification failed: %v", err))
		return
	}

	// Verify tenant ID matches JWT claims
	if claims.Sub != regPayload.TenantID {
		log.Printf("Tenant ID mismatch: expected %s, got %s", claims.Sub, regPayload.TenantID)
		s.sendError(stream, "TENANT_ID_MISMATCH", "Tenant ID does not match JWT claims")
		return
	}

	log.Printf("✅ Agent authenticated: tenantId=%s, organization=%s, version=%s",
		regPayload.TenantID, claims.OrganizationID, regPayload.Version)

	// Allocate port and create tenant
	tenant := s.registerTenant(regPayload.TenantID, session)
	if tenant == nil {
		log.Printf("Failed to register tenant: %s", regPayload.TenantID)
		s.sendError(stream, "REGISTRATION_FAILED", "Failed to allocate port")
		return
	}

	// Send registration response
	response := common.RegisteredPayload{
		TenantID:     tenant.ID,
		AssignedPort: tenant.AssignedPort,
		SQLUser:      tenant.SQLUser,
		SQLPassword:  tenant.SQLPassword,
		PublicHost:   "link.tatbeeb.sa", // From config
		ConnectionString: fmt.Sprintf(
			"Server=link.tatbeeb.sa,%d;Encrypt=True;TrustServerCertificate=False;User Id=%s;Password=%s;",
			tenant.AssignedPort,
			tenant.SQLUser,
			tenant.SQLPassword,
		),
	}

	respData, _ := common.EncodeMessage(common.MsgTypeRegistered, response)
	if _, err := stream.Write(respData); err != nil {
		log.Printf("Failed to send registration response: %v", err)
		s.unregisterTenant(tenant.ID)
		return
	}

	log.Printf("Tenant %s assigned port %d", tenant.ID, tenant.AssignedPort)

	// Notify HIS backend about assigned port
	go func() {
		if err := s.hisClient.RegisterPort(tenant.ID, tenant.AssignedPort); err != nil {
			log.Printf("⚠️  Failed to register port with HIS for tenant %s: %v", tenant.ID, err)
		} else {
			log.Printf("✅ Port registered with HIS for tenant %s", tenant.ID)
		}
	}()

	// Start accepting SQL connections for this tenant
	go s.acceptTenantConnections(tenant)

	// Start heartbeat to HIS
	go s.sendHeartbeats(tenant)

	// Keep control stream alive with heartbeat
	s.keepAlive(stream, tenant)
}

func (s *RelayServer) registerTenant(tenantID string, session *yamux.Session) *Tenant {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already registered
	if existing, ok := s.tenants[tenantID]; ok {
		// Close old session
		if existing.Listener != nil {
			existing.Listener.Close()
		}
		log.Printf("Tenant %s re-registering", tenantID)
	}

	// Allocate port
	if s.nextPortIndex >= len(s.portPool) {
		log.Printf("No ports available")
		return nil
	}

	port := s.portPool[s.nextPortIndex]
	s.nextPortIndex++

	// Start listener for this tenant
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Printf("Failed to start listener on port %d: %v", port, err)
		return nil
	}

	tenant := &Tenant{
		ID:             tenantID,
		AssignedPort:   port,
		SQLUser:        fmt.Sprintf("tatbeeb_%s", tenantID[:6]),
		SQLPassword:    generatePassword(),
		ControlSession: session,
		Listener:       listener,
	}

	s.tenants[tenantID] = tenant
	return tenant
}

func (s *RelayServer) unregisterTenant(tenantID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tenant, ok := s.tenants[tenantID]; ok {
		if tenant.Listener != nil {
			tenant.Listener.Close()
		}
		delete(s.tenants, tenantID)
		log.Printf("Tenant %s unregistered", tenantID)
	}
}

func (s *RelayServer) acceptTenantConnections(tenant *Tenant) {
	defer s.unregisterTenant(tenant.ID)

	for {
		conn, err := tenant.Listener.Accept()
		if err != nil {
			log.Printf("Tenant %s listener error: %v", tenant.ID, err)
			return
		}

		// Check connection limit
		tenant.mu.Lock()
		if tenant.ActiveConns >= s.config.MaxConnectionsPerTenant {
			tenant.mu.Unlock()
			log.Printf("Tenant %s connection limit reached", tenant.ID)
			conn.Close()
			continue
		}
		tenant.ActiveConns++
		tenant.mu.Unlock()

		go s.handleTenantConnection(tenant, conn)
	}
}

func (s *RelayServer) handleTenantConnection(tenant *Tenant, clientConn net.Conn) {
	defer clientConn.Close()
	defer func() {
		tenant.mu.Lock()
		tenant.ActiveConns--
		tenant.mu.Unlock()
	}()

	// Open new stream to agent
	stream, err := tenant.ControlSession.OpenStream()
	if err != nil {
		log.Printf("Failed to open stream to agent: %v", err)
		return
	}
	defer stream.Close()

	log.Printf("Forwarding connection for tenant %s", tenant.ID)

	// Bidirectional copy
	done := make(chan error, 2)

	go func() {
		_, err := io.Copy(stream, clientConn)
		done <- err
	}()

	go func() {
		_, err := io.Copy(clientConn, stream)
		done <- err
	}()

	// Wait for either direction to complete
	<-done
}

func (s *RelayServer) sendHeartbeats(tenant *Tenant) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C

		// Check if tenant still exists
		s.mu.RLock()
		_, exists := s.tenants[tenant.ID]
		s.mu.RUnlock()

		if !exists {
			log.Printf("Tenant %s no longer exists, stopping heartbeat", tenant.ID)
			return
		}

		// Send heartbeat to HIS
		if err := s.hisClient.SendHeartbeat(tenant.ID); err != nil {
			log.Printf("⚠️  Failed to send heartbeat to HIS for tenant %s: %v", tenant.ID, err)
		}
	}
}

func (s *RelayServer) keepAlive(stream net.Conn, tenant *Tenant) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C

		// Send ping
		pingData, _ := common.EncodeMessage(common.MsgTypePing, nil)
		stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := stream.Write(pingData); err != nil {
			log.Printf("Tenant %s ping failed: %v", tenant.ID, err)
			s.unregisterTenant(tenant.ID)
			return
		}
	}
}

func (s *RelayServer) sendError(stream net.Conn, code, message string) {
	errPayload := common.ErrorPayload{
		Code:    code,
		Message: message,
	}
	errData, _ := common.EncodeMessage(common.MsgTypeError, errPayload)
	stream.Write(errData)
}

func generatePassword() string {
	// TODO: Implement secure password generation
	return fmt.Sprintf("pwd_%d", time.Now().Unix())
}

func main() {
	configFile := flag.String("config", "config.production.json", "Path to config file")
	flag.Parse()

	log.Printf("🟦 Tatbeeb Link Relay Server v1.0.0")
	log.Printf("Loading configuration from: %s", *configFile)

	// Load JSON configuration
	configData, err := ioutil.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("Failed to read config file: %v", err)
	}

	var fullConfig struct {
		Server struct {
			ControlPort             int `json:"controlPort"`
			TenantPortStart         int `json:"tenantPortStart"`
			TenantPortEnd           int `json:"tenantPortEnd"`
			MaxConnectionsPerTenant int `json:"maxConnectionsPerTenant"`
		} `json:"server"`
		TLS struct {
			CertFile string `json:"certFile"`
			KeyFile  string `json:"keyFile"`
		} `json:"tls"`
		JWT struct {
			Secret string `json:"secret"`
		} `json:"jwt"`
		HIS struct {
			BackendURL        string `json:"backendUrl"`
			RelaySharedSecret string `json:"relaySharedSecret"`
		} `json:"his"`
	}

	if err := json.Unmarshal(configData, &fullConfig); err != nil {
		log.Fatalf("Failed to parse config file: %v", err)
	}

	// Create relay config
	config := &common.RelayConfig{
		ControlPort:             fullConfig.Server.ControlPort,
		TenantPortStart:         fullConfig.Server.TenantPortStart,
		TenantPortEnd:           fullConfig.Server.TenantPortEnd,
		MaxConnectionsPerTenant: fullConfig.Server.MaxConnectionsPerTenant,
		TLSCertFile:             fullConfig.TLS.CertFile,
		TLSKeyFile:              fullConfig.TLS.KeyFile,
	}

	// Validate configuration
	if config.TLSCertFile == "" {
		log.Fatal("TLS certificate file required (set tls.certFile in config)")
	}
	if config.TLSKeyFile == "" {
		log.Fatal("TLS key file required (set tls.keyFile in config)")
	}
	if fullConfig.JWT.Secret == "" {
		log.Fatal("JWT secret required (set jwt.secret in config)")
	}
	if fullConfig.HIS.RelaySharedSecret == "" {
		log.Fatal("Relay shared secret required (set his.relaySharedSecret in config)")
	}

	log.Printf("✅ Configuration loaded successfully")
	log.Printf("   HIS Backend: %s", fullConfig.HIS.BackendURL)
	log.Printf("   Control Port: %d", config.ControlPort)
	log.Printf("   Tenant Ports: %d-%d", config.TenantPortStart, config.TenantPortEnd)

	// Create and start server
	server := NewRelayServer(
		config,
		fullConfig.HIS.BackendURL,
		fullConfig.HIS.RelaySharedSecret,
		fullConfig.JWT.Secret,
	)

	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start relay server: %v", err)
	}
}
