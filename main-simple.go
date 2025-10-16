package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Config struct {
	ControlPort     int    `json:"controlPort"`
	TLSCertFile     string `json:"tlsCertFile"`
	TLSKeyFile      string `json:"tlsKeyFile"`
	PublicHost      string `json:"publicHost"`
	TenantPortStart int    `json:"tenantPortStart"`
	TenantPortEnd   int    `json:"tenantPortEnd"`
}

type Agent struct {
	conn          net.Conn
	assignedPort  int
	listener      net.Listener
	lastHeartbeat time.Time
	mu            sync.Mutex
}

type RelayServer struct {
	config         Config
	agents         map[int]*Agent
	availablePorts []int
	mu             sync.RWMutex
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	configPath := "/etc/tatbeeb-link/config.production.json"
	if len(os.Args) > 2 && os.Args[1] == "-config" {
		configPath = os.Args[2]
	}

	configFile, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}

	var config Config
	if err := json.Unmarshal(configFile, &config); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}

	log.Printf("🚀 Tatbeeb Link Relay Server starting...")
	log.Printf("📝 Config: %s", configPath)
	log.Printf("🔐 TLS Cert: %s", config.TLSCertFile)
	log.Printf("🌐 Public Host: %s", config.PublicHost)
	log.Printf("📡 Control Port: %d", config.ControlPort)
	log.Printf("🎯 Port Range: %d-%d", config.TenantPortStart, config.TenantPortEnd)

	relay := &RelayServer{
		config:         config,
		agents:         make(map[int]*Agent),
		availablePorts: make([]int, 0),
	}

	// Initialize available ports
	for port := config.TenantPortStart; port <= config.TenantPortEnd; port++ {
		relay.availablePorts = append(relay.availablePorts, port)
	}

	// Load TLS certificate
	cert, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
	if err != nil {
		log.Fatalf("Failed to load TLS certificate: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Start control port listener
	listener, err := tls.Listen("tcp", fmt.Sprintf(":%d", config.ControlPort), tlsConfig)
	if err != nil {
		log.Fatalf("Failed to start control listener: %v", err)
	}
	defer listener.Close()

	log.Printf("✅ Relay server listening on port %d (TLS)", config.ControlPort)

	// Start health check HTTP server
	go relay.startHealthServer()

	// Start heartbeat checker
	go relay.heartbeatChecker()

	// Accept connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		go relay.handleAgent(conn)
	}
}

func (r *RelayServer) handleAgent(conn net.Conn) {
	defer conn.Close()

	clientAddr := conn.RemoteAddr().String()
	log.Printf("📥 New connection from %s", clientAddr)

	reader := bufio.NewReader(conn)

	// Read first line (should be REGISTER)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Printf("Failed to read from %s: %v", clientAddr, err)
		return
	}

	line = strings.TrimSpace(line)
	if line != "REGISTER" {
		log.Printf("Invalid command from %s: %s", clientAddr, line)
		conn.Write([]byte("ERROR invalid command\n"))
		return
	}

	// Assign a port
	port := r.assignPort()
	if port == 0 {
		log.Printf("No available ports for %s", clientAddr)
		conn.Write([]byte("ERROR no available ports\n"))
		return
	}

	// Create agent
	agent := &Agent{
		conn:          conn,
		assignedPort:  port,
		lastHeartbeat: time.Now(),
	}

	// Start listener for assigned port
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Printf("Failed to create listener on port %d: %v", port, err)
		r.releasePort(port)
		conn.Write([]byte(fmt.Sprintf("ERROR failed to bind port: %v\n", err)))
		return
	}

	agent.listener = listener

	// Store agent
	r.mu.Lock()
	r.agents[port] = agent
	r.mu.Unlock()

	// Send success response
	response := fmt.Sprintf("OK port:%d\n", port)
	_, err = conn.Write([]byte(response))
	if err != nil {
		log.Printf("Failed to send response to %s: %v", clientAddr, err)
		r.cleanupAgent(port)
		return
	}

	log.Printf("✅ Registered agent from %s on port %d", clientAddr, port)

	// Start accepting connections on assigned port
	go r.acceptConnections(agent)

	// Handle heartbeats
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("Connection lost from %s: %v", clientAddr, err)
			}
			break
		}

		line = strings.TrimSpace(line)
		if line == "HEARTBEAT" {
			agent.mu.Lock()
			agent.lastHeartbeat = time.Now()
			agent.mu.Unlock()
		}
	}

	// Cleanup
	log.Printf("🔌 Agent on port %d disconnected", port)
	r.cleanupAgent(port)
}

func (r *RelayServer) acceptConnections(agent *Agent) {
	for {
		clientConn, err := agent.listener.Accept()
		if err != nil {
			// Listener closed
			return
		}

		log.Printf("📞 Client connected to port %d", agent.assignedPort)
		go r.proxyConnection(agent, clientConn)
	}
}

func (r *RelayServer) proxyConnection(agent *Agent, clientConn net.Conn) {
	defer clientConn.Close()

	// For now, just close it since we don't have the agent connection forwarding
	// In a full implementation, you'd forward to the agent's SQL Server
	log.Printf("⚠️ Proxy not implemented yet - closing connection on port %d", agent.assignedPort)
}

func (r *RelayServer) assignPort() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.availablePorts) == 0 {
		return 0
	}

	port := r.availablePorts[0]
	r.availablePorts = r.availablePorts[1:]
	return port
}

func (r *RelayServer) releasePort(port int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.availablePorts = append(r.availablePorts, port)
}

func (r *RelayServer) cleanupAgent(port int) {
	r.mu.Lock()
	agent, exists := r.agents[port]
	delete(r.agents, port)
	r.mu.Unlock()

	if exists {
		if agent.listener != nil {
			agent.listener.Close()
		}
		if agent.conn != nil {
			agent.conn.Close()
		}
		r.releasePort(port)
	}
}

func (r *RelayServer) heartbeatChecker() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		r.mu.RLock()
		ports := make([]int, 0)
		for port, agent := range r.agents {
			agent.mu.Lock()
			if time.Since(agent.lastHeartbeat) > 120*time.Second {
				ports = append(ports, port)
			}
			agent.mu.Unlock()
		}
		r.mu.RUnlock()

		// Cleanup stale agents
		for _, port := range ports {
			log.Printf("⚠️ Agent on port %d timed out", port)
			r.cleanupAgent(port)
		}
	}
}

func (r *RelayServer) startHealthServer() {
	http.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		r.mu.RLock()
		activeAgents := len(r.agents)
		availablePorts := len(r.availablePorts)
		r.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "healthy",
			"activeAgents":   activeAgents,
			"availablePorts": availablePorts,
		})
	})

	log.Printf("🏥 Health check server on :8080/health")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Printf("Health server error: %v", err)
	}
}
