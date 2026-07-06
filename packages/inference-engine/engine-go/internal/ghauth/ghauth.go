// Package ghauth mints and caches GitHub App installation access tokens.
package ghauth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultAppID          = "3827239"
	defaultInstallationID = "134948616"
	defaultKeyPath        = "/home/patrik/.github-app-key.pem"
	githubAPI             = "https://api.github.com"
	tokenTTL              = 50 * time.Minute
	jwtLifetime           = 10 * time.Minute
)

// TokenProvider is the interface for fetching installation tokens.
type TokenProvider interface {
	GetToken() (string, error)
}

// Config holds ghauth configuration.
type Config struct {
	AppID          string
	InstallationID string
	PrivateKeyPath string
	APIBase        string
}

// DefaultConfig returns configuration from well-known defaults and environment.
func DefaultConfig() Config {
	c := Config{
		AppID:          defaultAppID,
		InstallationID: defaultInstallationID,
		PrivateKeyPath: defaultKeyPath,
		APIBase:        githubAPI,
	}
	if v := os.Getenv("GITHUB_APP_ID"); v != "" {
		c.AppID = v
	}
	if v := os.Getenv("GITHUB_APP_INSTALLATION_ID"); v != "" {
		c.InstallationID = v
	}
	if v := os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"); v != "" {
		c.PrivateKeyPath = v
	}
	if v := os.Getenv("GITHUB_API_BASE"); v != "" {
		c.APIBase = v
	}
	return c
}

// Client mints JWTs and exchanges them for installation access tokens.
type Client struct {
	config    Config
	key       *rsa.PrivateKey
	client    *http.Client
	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

// NewClient creates a ghauth client from config.
func NewClient(config Config) (*Client, error) {
	key, err := loadPrivateKey(config.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load private key: %w", err)
	}
	return &Client{
		config: config,
		key:    key,
		client: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// NewDefaultClient creates a client using DefaultConfig.
func NewDefaultClient() (*Client, error) {
	return NewClient(DefaultConfig())
}

// GetToken returns a valid installation access token, refreshing if necessary.
func (c *Client) GetToken() (string, error) {
	c.mu.RLock()
	token := c.token
	expiresAt := c.expiresAt
	c.mu.RUnlock()

	// Refresh with a 5-minute safety margin.
	if token != "" && time.Until(expiresAt) > 5*time.Minute {
		return token, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock.
	if c.token != "" && time.Until(c.expiresAt) > 5*time.Minute {
		return c.token, nil
	}

	token, expiresAt, err := c.fetchToken()
	if err != nil {
		// Fallback: return existing token if not yet expired.
		if c.token != "" && time.Now().Before(c.expiresAt) {
			return c.token, nil
		}
		return "", err
	}

	c.token = token
	c.expiresAt = expiresAt
	return token, nil
}

// fetchToken mints a JWT and exchanges it for an installation token.
func (c *Client) fetchToken() (string, time.Time, error) {
	jwtToken, err := c.mintJWT()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("mint jwt: %w", err)
	}

	installationID, err := strconv.ParseInt(c.config.InstallationID, 10, 64)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse installation id: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.config.APIBase, installationID)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		// Read limited response for error context.
		body := make([]byte, 4096)
		n, _ := resp.Body.Read(body)
		return "", time.Time{}, fmt.Errorf("token request failed: %s: %s", resp.Status, string(body[:n]))
	}

	var result struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", time.Time{}, fmt.Errorf("decode token response: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil {
		// If parsing fails, use our conservative TTL from now.
		expiresAt = time.Now().Add(tokenTTL)
	}

	return result.Token, expiresAt, nil
}

// mintJWT creates a signed JWT for the GitHub App.
func (c *Client) mintJWT() (string, error) {
	appID, err := strconv.ParseInt(c.config.AppID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("parse app id: %w", err)
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(), // 60s clock skew leeway
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": appID,
	})

	signed, err := token.SignedString(c.key)
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signed, nil
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no pem block found")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8
		keyInterface, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		var ok bool
		key, ok = keyInterface.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
	}
	return key, nil
}
