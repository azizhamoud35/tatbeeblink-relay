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

	// Read simple text protocol: "REGISTER\n" byte-by-byte to avoid buffering
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var line strings.Builder
	buf := make([]byte, 1)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			log.Printf("Failed to read command: %v", err)
			return
		}
		if buf[0] == '\n' {
			break
		}
		line.WriteByte(buf[0])
	}
	conn.SetReadDeadline(time.Time{})

	command := strings.TrimSpace(line.String())
	if command != "REGISTER" {
		log.Printf("Unknown command: %s", command)
		return
	}

	// Allocate port
	s.mu.Lock()
	if s.nextPortIndex >= len(s.portPool) {
		s.mu.Unlock()
		conn.Write([]byte("ERROR No ports available\n"))
		return
	}
	port := s.portPool[s.nextPortIndex]
	s.nextPortIndex++
	tenantID := fmt.Sprintf("tenant-%d", port)
	s.mu.Unlock()

	// Send response
	response := fmt.Sprintf("OK port:%d\n", port)
	if _, err := conn.Write([]byte(response)); err != nil {
		log.Printf("Failed to send response: %v", err)
		return
	}

	log.Printf("✅ Registered tenant %s on port %d", tenantID, port)

	// Create yamux session
	session, err := yamux.Server(conn, nil)
	if err != nil {
		log.Printf("Failed to create yamux session: %v", err)
		return
	}
	defer session.Close()

	// Start TCP listener on assigned port
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Printf("Failed to listen on port %d: %v", port, err)
		return
	}
	defer listener.Close()

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
		log.Printf("Unregistered tenant %s", tenantID)
	}()

	log.Printf("🎧 Listening on port %d for connections", port)

	// Accept client connections and forward through yamux
	for {
		clientConn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting client connection: %v", err)
			return
		}

		go s.handleClientConnection(clientConn, tenant)
	}
}

func (s *SimpleRelay) handleClientConnection(clientConn net.Conn, tenant *SimpleTenant) {
	defer clientConn.Close()

	log.Printf("🔗 Client connected to port %d, opening stream...", tenant.AssignedPort)

	// Open a new stream to the agent
	stream, err := tenant.YamuxSession.OpenStream()
	if err != nil {
		log.Printf("Failed to open stream: %v", err)
		return
	}
	defer stream.Close()

	tenant.mu.Lock()
	tenant.ActiveConns++
	tenant.mu.Unlock()

	defer func() {
		tenant.mu.Lock()
		tenant.ActiveConns--
		tenant.mu.Unlock()
	}()

	log.Printf("✅ Stream opened, forwarding data...")

	// Forward data bidirectionally
	done := make(chan bool, 2)

	// Client -> Agent
	go func() {
		io.Copy(stream, clientConn)
		done <- true
	}()

	// Agent -> Client
	go func() {
		io.Copy(clientConn, stream)
		done <- true
	}()

	<-done
	log.Printf("Connection closed for port %d", tenant.AssignedPort)
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
