package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type SimpleTenant struct {
	ID           string
	AssignedPort int
	YamuxSession *yamux.Session
	Listener     net.Listener
	ActiveConns  int
	mu           sync.Mutex
}

type SimpleRelay struct {
	tenants       map[string]*SimpleTenant
	portPool      []int
	nextPortIndex int
	mu            sync.RWMutex
}

func NewSimpleRelay(startPort, endPort int) *SimpleRelay {
	portPool := make([]int, 0, endPort-startPort+1)
	for p := startPort; p <= endPort; p++ {
		portPool = append(portPool, p)
	}

	return &SimpleRelay{
		tenants:  make(map[string]*SimpleTenant),
		portPool: portPool,
	}
}

func (s *SimpleRelay) Start() error {
	// Start health check
	go s.startHealthCheck()

	// Load TLS certificate
	cert, err := tls.LoadX509KeyPair(
		"/etc/letsencrypt/live/link.tatbeeb.sa/fullchain.pem",
		"/etc/letsencrypt/live/link.tatbeeb.sa/privkey.pem",
	)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Start control listener
	listener, err := tls.Listen("tcp", ":8443", tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	log.Printf("🚀 Simple Tatbeeb Link Relay started")
	log.Printf("   Control port: 8443 (TLS)")
	log.Printf("   Tenant ports: 50000-50999")
	log.Printf("   Health check: http://localhost:9090/health")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		go s.handleConnection(conn)
	}
}

func (s *SimpleRelay) handleConnection(conn net.Conn) {
	defer conn.Close()

	clientAddr := conn.RemoteAddr().String()
	log.Printf("🔌 New connection from %s", clientAddr)

	// Read simple text protocol: "REGISTER\n" byte-by-byte to avoid buffering
	log.Printf("📖 [%s] Reading REGISTER command...", clientAddr)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var line strings.Builder
	buf := make([]byte, 1)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			log.Printf("❌ [%s] Failed to read command: %v", clientAddr, err)
			return
		}
		if buf[0] == '\n' {
			break
		}
		line.WriteByte(buf[0])
	}
	conn.SetReadDeadline(time.Time{})

	command := strings.TrimSpace(line.String())
	log.Printf("📝 [%s] Received command: '%s'", clientAddr, command)
	
	if command != "REGISTER" {
		log.Printf("❌ [%s] Unknown command: %s", clientAddr, command)
		return
	}

	// Allocate port
	log.Printf("🔢 [%s] Allocating port...", clientAddr)
	s.mu.Lock()
	if s.nextPortIndex >= len(s.portPool) {
		s.mu.Unlock()
		log.Printf("❌ [%s] No ports available (used %d/%d)", clientAddr, s.nextPortIndex, len(s.portPool))
		conn.Write([]byte("ERROR No ports available\n"))
		return
	}
	port := s.portPool[s.nextPortIndex]
	s.nextPortIndex++
	tenantID := fmt.Sprintf("tenant-%d", port)
	s.mu.Unlock()

	log.Printf("✅ [%s] Allocated port %d (tenant: %s)", clientAddr, port, tenantID)

	// Send response
	response := fmt.Sprintf("OK port:%d\n", port)
	log.Printf("📤 [%s] Sending response: '%s'", clientAddr, strings.TrimSpace(response))
	if _, err := conn.Write([]byte(response)); err != nil {
		log.Printf("❌ [%s] Failed to send response: %v", clientAddr, err)
		return
	}

	log.Printf("✅ [%s] Response sent successfully", clientAddr)

	// Create yamux session
	log.Printf("🔀 [%s] Creating yamux session...", clientAddr)
	session, err := yamux.Server(conn, nil)
	if err != nil {
		log.Printf("❌ [%s] Failed to create yamux session: %v", clientAddr, err)
		return
	}
	defer session.Close()
	log.Printf("✅ [%s] Yamux session created successfully", clientAddr)

	// Start TCP listener on assigned port
	log.Printf("🎧 [%s] Starting listener on port %d...", clientAddr, port)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Printf("❌ [%s] Failed to listen on port %d: %v", clientAddr, port, err)
		return
	}
	defer listener.Close()
	log.Printf("✅ [%s] Listening on port %d", clientAddr, port)

	tenant := &SimpleTenant{
		ID:           tenantID,
		AssignedPort: port,
		YamuxSession: session,
		Listener:     listener,
	}

	s.mu.Lock()
	s.tenants[tenantID] = tenant
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.tenants, tenantID)
		s.mu.Unlock()
		log.Printf("🔌 [%s] Unregistered tenant %s (port %d)", clientAddr, tenantID, port)
	}()

	log.Printf("🎧 [%s] Ready! Waiting for client connections on port %d...", clientAddr, port)

	// Accept client connections and forward through yamux
	connCount := 0
	for {
		clientConn, err := listener.Accept()
		if err != nil {
			log.Printf("❌ [%s] Error accepting client connection: %v", clientAddr, err)
			return
		}

		connCount++
		log.Printf("🔗 [%s] Client connection #%d received on port %d", clientAddr, connCount, port)

		go s.handleClientConnection(clientConn, tenant, connCount)
	}
}

func (s *SimpleRelay) handleClientConnection(clientConn net.Conn, tenant *SimpleTenant, connNum int) {
	defer clientConn.Close()

	clientAddr := clientConn.RemoteAddr().String()
	log.Printf("🔗 [Conn#%d] Client %s connected to port %d", connNum, clientAddr, tenant.AssignedPort)

	// Open a new stream to the agent
	log.Printf("📡 [Conn#%d] Opening yamux stream to agent...", connNum)
	stream, err := tenant.YamuxSession.OpenStream()
	if err != nil {
		log.Printf("❌ [Conn#%d] Failed to open stream: %v", connNum, err)
		return
	}
	defer stream.Close()
	log.Printf("✅ [Conn#%d] Yamux stream opened", connNum)

	tenant.mu.Lock()
	tenant.ActiveConns++
	activeConns := tenant.ActiveConns
	tenant.mu.Unlock()
	log.Printf("📊 [Conn#%d] Active connections: %d", connNum, activeConns)

	defer func() {
		tenant.mu.Lock()
		tenant.ActiveConns--
		activeConns := tenant.ActiveConns
		tenant.mu.Unlock()
		log.Printf("📊 [Conn#%d] Connection closed. Remaining: %d", connNum, activeConns)
	}()

	log.Printf("🔄 [Conn#%d] Starting bidirectional data forwarding...", connNum)

	// Forward data bidirectionally
	done := make(chan bool, 2)

	// Client -> Agent
	go func() {
		n, err := io.Copy(stream, clientConn)
		if err != nil {
			log.Printf("⚠️ [Conn#%d] Client->Agent error: %v", connNum, err)
		}
		log.Printf("📤 [Conn#%d] Client->Agent: %d bytes", connNum, n)
		done <- true
	}()

	// Agent -> Client
	go func() {
		n, err := io.Copy(clientConn, stream)
		if err != nil {
			log.Printf("⚠️ [Conn#%d] Agent->Client error: %v", connNum, err)
		}
		log.Printf("📥 [Conn#%d] Agent->Client: %d bytes", connNum, n)
		done <- true
	}()

	<-done
	log.Printf("🔌 [Conn#%d] Connection finished (port %d)", connNum, tenant.AssignedPort)
}

func (s *SimpleRelay) startHealthCheck() {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		activeTenants := len(s.tenants)
		s.mu.RUnlock()

		health := map[string]interface{}{
			"status":        "ok",
			"version":       "2.0.0-simple",
			"activeTenants": activeTenants,
			"timestamp":     time.Now().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(health)
	})

	log.Printf("Health check server listening on :9090")
	if err := http.ListenAndServe(":9090", nil); err != nil {
		log.Printf("Health check server error: %v", err)
	}
}

func main() {
	relay := NewSimpleRelay(50000, 50999)
	if err := relay.Start(); err != nil {
		log.Fatalf("Failed to start relay: %v", err)
	}
}
