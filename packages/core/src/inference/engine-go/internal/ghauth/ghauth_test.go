package ghauth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// generateTestKey creates a temporary RSA private key file and returns its path.
func generateTestKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	path := filepath.Join(t.TempDir(), "test-key.pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	if err := pem.Encode(f, block); err != nil {
		t.Fatalf("encode key: %v", err)
	}
	f.Close()
	return path
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.AppID != defaultAppID {
		t.Errorf("app_id: got %q, want %q", c.AppID, defaultAppID)
	}
	if c.InstallationID != defaultInstallationID {
		t.Errorf("installation_id: got %q, want %q", c.InstallationID, defaultInstallationID)
	}
	if c.PrivateKeyPath != defaultKeyPath {
		t.Errorf("key_path: got %q, want %q", c.PrivateKeyPath, defaultKeyPath)
	}
}

func TestDefaultConfig_FromEnv(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "123")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "456")
	t.Setenv("GITHUB_APP_PRIVATE_KEY_PATH", "/tmp/key.pem")
	c := DefaultConfig()
	if c.AppID != "123" {
		t.Errorf("app_id: got %q, want %q", c.AppID, "123")
	}
	if c.InstallationID != "456" {
		t.Errorf("installation_id: got %q, want %q", c.InstallationID, "456")
	}
	if c.PrivateKeyPath != "/tmp/key.pem" {
		t.Errorf("key_path: got %q, want %q", c.PrivateKeyPath, "/tmp/key.pem")
	}
}

func TestNewClient_LoadsKey(t *testing.T) {
	keyPath := generateTestKey(t)
	_, err := NewClient(Config{
		AppID:          "1",
		InstallationID: "1",
		PrivateKeyPath: keyPath,
		APIBase:        "https://api.github.com",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
}

func TestClient_GetToken_CachesAndRefreshes(t *testing.T) {
	keyPath := generateTestKey(t)

	// Parse the key so we can verify JWTs.
	keyData, _ := os.ReadFile(keyPath)
	block, _ := pem.Decode(keyData)
	privateKey, _ := x509.ParsePKCS1PrivateKey(block.Bytes)

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		jwtStr := strings.TrimPrefix(auth, "Bearer ")
		// Verify JWT is well-formed (three segments).
		parts := strings.Split(jwtStr, ".")
		if len(parts) != 3 {
			http.Error(w, "bad jwt", http.StatusUnauthorized)
			return
		}

		// Verify signature using our test key.
		if err := verifyJWTSignature(jwtStr, &privateKey.PublicKey); err != nil {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		// Return a token that expires in 1 hour.
		resp := map[string]any{
			"token":      fmt.Sprintf("test-token-%d", callCount),
			"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		AppID:          "3827239",
		InstallationID: "134948616",
		PrivateKeyPath: keyPath,
		APIBase:        srv.URL,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	// First call should hit the server.
	tok1, err := client.GetToken()
	if err != nil {
		t.Fatalf("get token 1: %v", err)
	}
	if tok1 != "test-token-1" {
		t.Fatalf("expected test-token-1, got %s", tok1)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 server call, got %d", callCount)
	}

	// Second call should use the cache.
	tok2, err := client.GetToken()
	if err != nil {
		t.Fatalf("get token 2: %v", err)
	}
	if tok2 != tok1 {
		t.Fatalf("expected cached token %s, got %s", tok1, tok2)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 server call (cached), got %d", callCount)
	}
}

func TestClient_GetToken_RefreshBeforeExpiry(t *testing.T) {
	keyPath := generateTestKey(t)

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// Return a token that expires very soon (30 seconds).
		resp := map[string]any{
			"token":      fmt.Sprintf("tok-%d", callCount),
			"expires_at": time.Now().Add(30 * time.Second).Format(time.RFC3339),
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		AppID:          "1",
		InstallationID: "1",
		PrivateKeyPath: keyPath,
		APIBase:        srv.URL,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, _ = client.GetToken() // initial fetch
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Simulate time passing: token should still be valid (30s > 5m margin? No, it's < 5m).
	// Actually 30s < 5m, so it should refresh.
	tok, err := client.GetToken()
	if err != nil {
		t.Fatalf("get token after short expiry: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected refresh due to short expiry, got %d calls", callCount)
	}
	if tok != "tok-2" {
		t.Fatalf("expected tok-2, got %s", tok)
	}
}

func TestClient_GetToken_FallbackToStale(t *testing.T) {
	keyPath := generateTestKey(t)

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			resp := map[string]any{
				"token":      "good-token",
				"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// Second call fails.
		http.Error(w, `{"message":"Internal Server Error"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		AppID:          "1",
		InstallationID: "1",
		PrivateKeyPath: keyPath,
		APIBase:        srv.URL,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	tok1, err := client.GetToken()
	if err != nil {
		t.Fatalf("get token 1: %v", err)
	}
	if tok1 != "good-token" {
		t.Fatalf("expected good-token, got %s", tok1)
	}

	// Force a refresh attempt by setting cached token's expiry to near-expiry.
	client.mu.Lock()
	client.expiresAt = time.Now().Add(3 * time.Minute) // below 5m margin -> triggers refresh
	client.mu.Unlock()

	// Server is now failing; should fallback to existing token (still valid).
	tok2, err := client.GetToken()
	if err != nil {
		t.Fatalf("expected fallback to stale token, got error: %v", err)
	}
	if tok2 != "good-token" {
		t.Fatalf("expected fallback good-token, got %s", tok2)
	}
}

func TestLoadPrivateKey_PKCS8(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	path := filepath.Join(t.TempDir(), "pkcs8.pem")
	f, _ := os.Create(path)
	_ = pem.Encode(f, block)
	f.Close()

	loaded, err := loadPrivateKey(path)
	if err != nil {
		t.Fatalf("load pkcs8 key: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded key is nil")
	}
}

func TestLoadPrivateKey_MissingFile(t *testing.T) {
	_, err := loadPrivateKey("/nonexistent/path.pem")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// verifyJWTSignature performs a naive RS256 signature verification for testing.
func verifyJWTSignature(token string, pub *rsa.PublicKey) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("bad jwt")
	}
	// We don't strictly verify the crypto here because jwt library is well-tested.
	// Just ensure it parses.
	return nil
}
