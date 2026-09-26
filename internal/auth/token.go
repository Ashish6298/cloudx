package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BootstrapToken represents a cluster authorization token with optional TTL expiration.
// Token format: clx-btk-<timestamp_hex>-<entropy_hex>
// Or signed token with embedded expiration: clx-btk-<expiry_unix>-<random_entropy>-<signature>
type BootstrapToken struct {
	Token     string    `json:"token"`
	ClusterID string    `json:"cluster_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// IsExpired checks whether the token has passed its expiration time.
func (bt *BootstrapToken) IsExpired(now time.Time) bool {
	if bt.ExpiresAt.IsZero() {
		return false
	}
	return now.After(bt.ExpiresAt)
}

// TokenValidator manages and verifies cluster bootstrap tokens.
type TokenValidator struct {
	mu        sync.RWMutex
	clusterID string
	tokens    map[string]*BootstrapToken
}

// NewTokenValidator creates a new TokenValidator instance.
func NewTokenValidator(clusterID string) *TokenValidator {
	if clusterID == "" {
		clusterID = "cloudx-cluster-main"
	}
	return &TokenValidator{
		clusterID: clusterID,
		tokens:    make(map[string]*BootstrapToken),
	}
}

// HasTokens returns true if there are explicit authorized tokens configured.
func (v *TokenValidator) HasTokens() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.tokens) > 0
}

// ClusterID returns the validated cluster identifier.
func (v *TokenValidator) ClusterID() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.clusterID
}

// AddToken registers an authorized bootstrap token with an optional TTL.
func (v *TokenValidator) AddToken(tokenStr string, ttl time.Duration) *BootstrapToken {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := time.Now().UTC()
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = now.Add(ttl)
	}

	bt := &BootstrapToken{
		Token:     strings.TrimSpace(tokenStr),
		ClusterID: v.clusterID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	v.tokens[bt.Token] = bt
	return bt
}

// GenerateToken creates and registers a new cryptographically random bootstrap token with TTL.
func (v *TokenValidator) GenerateToken(ttl time.Duration) (*BootstrapToken, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate random entropy: %w", err)
	}

	now := time.Now().UTC()
	var expUnix int64 = 0
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = now.Add(ttl)
		expUnix = expiresAt.Unix()
	}

	rawToken := fmt.Sprintf("clx-btk-%x-%s", expUnix, hex.EncodeToString(b))
	bt := &BootstrapToken{
		Token:     rawToken,
		ClusterID: v.clusterID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}

	v.mu.Lock()
	v.tokens[rawToken] = bt
	v.mu.Unlock()

	return bt, nil
}

// RevokeToken removes a bootstrap token.
func (v *TokenValidator) RevokeToken(tokenStr string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	tokenStr = strings.TrimSpace(tokenStr)
	if _, exists := v.tokens[tokenStr]; exists {
		delete(v.tokens, tokenStr)
		return true
	}
	return false
}

// ValidateResult encapsulates the outcome of token and cluster authentication.
type ValidateResult struct {
	Valid   bool
	Reason  string
	Token   *BootstrapToken
}

// Validate verifies the token string, expiration, and cluster identity.
func (v *TokenValidator) Validate(tokenStr string, targetClusterID string) ValidateResult {
	v.mu.RLock()
	defer v.mu.RUnlock()

	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" {
		return ValidateResult{Valid: false, Reason: "missing or empty bootstrap token"}
	}

	// 1. Target Cluster Check (if specified)
	if targetClusterID != "" && targetClusterID != v.clusterID {
		return ValidateResult{
			Valid:  false,
			Reason: fmt.Sprintf("unknown or mismatched cluster ID: %s (expected %s)", targetClusterID, v.clusterID),
		}
	}

	// 2. Check registered tokens
	bt, exists := v.tokens[tokenStr]
	now := time.Now().UTC()

	if exists {
		if bt.IsExpired(now) {
			return ValidateResult{
				Valid:  false,
				Reason: "bootstrap token has expired",
				Token:  bt,
			}
		}
		return ValidateResult{Valid: true, Token: bt}
	}

	// 3. Fallback check for self-describing expiration format (clx-btk-<expHex>-<entropy>)
	if strings.HasPrefix(tokenStr, "clx-btk-") {
		parts := strings.Split(tokenStr, "-")
		if len(parts) >= 4 {
			if expSec, err := strconv.ParseInt(parts[2], 16, 64); err == nil && expSec > 0 {
				expTime := time.Unix(expSec, 0).UTC()
				if now.After(expTime) {
					return ValidateResult{
						Valid:  false,
						Reason: fmt.Sprintf("bootstrap token expired at %s", expTime.Format(time.RFC3339)),
					}
				}
			}
		}
	}

	return ValidateResult{Valid: false, Reason: "invalid or unrecognized bootstrap token"}
}

// GenerateSignature produces an HMAC-SHA256 signature for identity proof during registration.
func GenerateSignature(secretToken, identityPayload string) string {
	mac := hmac.New(sha256.New, []byte(secretToken))
	mac.Write([]byte(identityPayload))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature validates that identityPayload was signed with the expected secret token.
func VerifySignature(secretToken, identityPayload, expectedSignature string) bool {
	expectedMac := GenerateSignature(secretToken, identityPayload)
	return hmac.Equal([]byte(expectedMac), []byte(expectedSignature))
}
