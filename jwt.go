package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JWT claims structure
type JWTClaims struct {
	Sub            string `json:"sub"` // Tenant ID
	Iss            string `json:"iss"` // Issuer
	Aud            string `json:"aud"` // Audience
	Exp            int64  `json:"exp"` // Expiry time
	Iat            int64  `json:"iat"` // Issued at
	OrganizationID string `json:"organizationId"`
	UserID         string `json:"userId"`
	Role           string `json:"role"`
}

// VerifyJWT verifies and decodes a JWT token
func VerifyJWT(tokenString, secret, expectedIssuer, expectedAudience string) (*JWTClaims, error) {
	// Split token into parts
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	header := parts[0]
	payload := parts[1]
	signature := parts[2]

	// Verify signature
	expectedSig, err := computeSignature(header, payload, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to compute signature: %w", err)
	}

	if signature != expectedSig {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
	}

	var claims JWTClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse claims: %w", err)
	}

	// Verify issuer
	if claims.Iss != expectedIssuer {
		return nil, fmt.Errorf("invalid issuer: expected %s, got %s", expectedIssuer, claims.Iss)
	}

	// Verify audience
	if claims.Aud != expectedAudience {
		return nil, fmt.Errorf("invalid audience: expected %s, got %s", expectedAudience, claims.Aud)
	}

	// Check expiration
	now := time.Now().Unix()
	if claims.Exp > 0 && claims.Exp < now {
		return nil, fmt.Errorf("token expired at %s", time.Unix(claims.Exp, 0).Format(time.RFC3339))
	}

	return &claims, nil
}

// computeSignature computes HMAC-SHA256 signature for JWT
func computeSignature(header, payload, secret string) (string, error) {
	message := header + "." + payload
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	sig := h.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(sig), nil
}
