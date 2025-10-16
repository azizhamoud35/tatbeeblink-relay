package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HISClient handles communication with HIS backend
type HISClient struct {
	baseURL     string
	relaySecret string
	httpClient  *http.Client
}

// NewHISClient creates a new HIS client
func NewHISClient(baseURL, relaySecret string) *HISClient {
	return &HISClient{
		baseURL:     baseURL,
		relaySecret: relaySecret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// RegisterPortRequest represents port registration request
type RegisterPortRequest struct {
	TenantID string `json:"tenantId"`
	Port     int    `json:"port"`
}

// RegisterPortResponse represents port registration response
type RegisterPortResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// RegisterPort registers an assigned port with HIS backend
func (c *HISClient) RegisterPort(tenantID string, port int) error {
	url := fmt.Sprintf("%s/api/v2/tatbeeb-link/register-port", c.baseURL)

	reqBody := RegisterPortRequest{
		TenantID: tenantID,
		Port:     port,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Relay-Secret", c.relaySecret)

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode != 200 {
		return fmt.Errorf("port registration failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var regResp RegisterPortResponse
	if err := json.Unmarshal(body, &regResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !regResp.Success {
		return fmt.Errorf("port registration failed: %s", regResp.Message)
	}

	return nil
}

// HeartbeatRequest represents heartbeat request
type HeartbeatRequest struct {
	TenantID string `json:"tenantId"`
}

// HeartbeatResponse represents heartbeat response
type HeartbeatResponse struct {
	Success bool `json:"success"`
}

// SendHeartbeat sends a heartbeat to HIS backend
func (c *HISClient) SendHeartbeat(tenantID string) error {
	url := fmt.Sprintf("%s/api/v2/tatbeeb-link/heartbeat", c.baseURL)

	reqBody := HeartbeatRequest{
		TenantID: tenantID,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Relay-Secret", c.relaySecret)

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("heartbeat failed (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
