// Package manager loads manager configuration.
package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds manager configuration.
type Config struct {
	ListenAddr   string   `json:"listen_addr"`
	RepoOwner    string   `json:"repo_owner"`
	RepoName     string   `json:"repo_name"`
	PollInterval string   `json:"poll_interval"`
	DaemonURL    string   `json:"daemon_url"`
	DaemonURLs   []string `json:"daemon_urls"`
	GatewayURL   string   `json:"gateway_url"`
	// RunGatewayURL is the gateway endpoint injected into dispatched run pods.
	// Run pods execute as k8s Jobs and cannot resolve the docker-compose service
	// name (e.g. http://gateway:4000); they must reach the gateway via a
	// cluster-routable address (node LAN IP). When empty it falls back to
	// GatewayURL. See ResolveRunGatewayURL.
	RunGatewayURL     string `json:"run_gateway_url"`
	DBPath            string `json:"db_path"`
	DefaultImage      string `json:"default_image"`
	BaseBranch        string `json:"base_branch"`
	Version           string `json:"version"`
	MaxConcurrentRuns int    `json:"max_concurrent_runs"`
	NodeID            string `json:"node_id"`
	NATSURL           string `json:"nats_url"`
}

// Default returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		ListenAddr:        ":8081",
		RepoOwner:         "marius-patrik",
		RepoName:          "agents",
		PollInterval:      "30s",
		DaemonURL:         "http://localhost:8080",
		DaemonURLs:        []string{"http://localhost:8080"},
		GatewayURL:        "http://localhost:4000",
		DBPath:            "agents_manager.db",
		DefaultImage:      "agents/harness:latest",
		BaseBranch:        "dev",
		Version:           "3.0.0",
		MaxConcurrentRuns: 4,
		NodeID:            os.Getenv("NODE_ID"),
		NATSURL:           os.Getenv("NATS_URL"),
	}
}

// FromEnv overlays environment variables onto defaults.
func FromEnv() Config {
	c := DefaultConfig()
	if v := firstEnv("AGENTS_MANAGER_ADDR", "AGENTS_ORCH_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := firstEnv("AGENTS_MANAGER_REPO_OWNER", "AGENTS_ORCH_REPO_OWNER"); v != "" {
		c.RepoOwner = v
	}
	if v := firstEnv("AGENTS_MANAGER_REPO_NAME", "AGENTS_ORCH_REPO_NAME"); v != "" {
		c.RepoName = v
	}
	if v := firstEnv("AGENTS_MANAGER_POLL", "AGENTS_ORCH_POLL"); v != "" {
		c.PollInterval = v
	}
	if v := firstEnv("AGENTS_MANAGER_DAEMON_URL", "AGENTS_ORCH_DAEMON_URL"); v != "" {
		if urls := parseDaemonURLs(v); len(urls) > 0 {
			c.DaemonURLs = urls
			c.DaemonURL = urls[0]
		}
	}
	if v := firstEnv("AGENTS_MANAGER_GATEWAY_URL", "AGENTS_ORCH_GATEWAY_URL"); v != "" {
		c.GatewayURL = v
	}
	if v := firstEnv("AGENTS_MANAGER_RUN_GATEWAY_URL", "AGENTS_ORCH_RUN_GATEWAY_URL"); v != "" {
		c.RunGatewayURL = v
	}
	if v := firstEnv("AGENTS_MANAGER_DB", "AGENTS_ORCH_DB"); v != "" {
		c.DBPath = v
	}
	if v := firstEnv("AGENTS_MANAGER_IMAGE", "AGENTS_HARNESS_IMAGE", "AGENTS_ORCH_IMAGE"); v != "" {
		c.DefaultImage = v
	}
	if v := firstEnv("AGENTS_MANAGER_BASE_BRANCH", "AGENTS_ORCH_BASE_BRANCH"); v != "" {
		c.BaseBranch = v
	}
	if v := firstEnv("AGENTS_MANAGER_VERSION", "AGENTS_ORCH_VERSION"); v != "" {
		c.Version = v
	}
	if v := os.Getenv("NODE_ID"); v != "" {
		c.NodeID = v
	}
	if v := os.Getenv("NATS_URL"); v != "" {
		c.NATSURL = v
	}
	return c
}

func parseDaemonURLs(raw string) []string {
	parts := strings.Split(raw, ",")
	urls := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			urls = append(urls, part)
		}
	}
	return urls
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

// FromFile loads JSON config from a path.
func FromFile(path string) (Config, error) {
	c := FromEnv()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse config: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return c, fmt.Errorf("parse config: %w", err)
	}
	if _, ok := raw["daemon_urls"]; ok {
		if len(c.DaemonURLs) > 0 {
			c.DaemonURL = c.DaemonURLs[0]
		}
	} else if _, ok := raw["daemon_url"]; ok {
		if urls := parseDaemonURLs(c.DaemonURL); len(urls) > 0 {
			c.DaemonURLs = urls
		}
	}
	return c, nil
}

// PollDuration parses the poll interval.
func (c Config) PollDuration() time.Duration {
	d, err := time.ParseDuration(c.PollInterval)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

// FullRepo returns "owner/name".
func (c Config) FullRepo() string {
	return c.RepoOwner + "/" + c.RepoName
}

// ResolveRunGatewayURL returns the gateway endpoint to inject into dispatched
// run pods. Run pods cannot resolve the compose service name, so deployments
// set RunGatewayURL to a cluster-routable address (node LAN IP); when unset it
// falls back to the manager's own GatewayURL.
func (c Config) ResolveRunGatewayURL() string {
	if strings.TrimSpace(c.RunGatewayURL) != "" {
		return c.RunGatewayURL
	}
	return c.GatewayURL
}

// ResolveMaxConcurrentRuns returns the effective max runs, applying env override.
func (c Config) ResolveMaxConcurrentRuns() int {
	if v := firstEnv("AGENTS_MANAGER_MAX_RUNS", "AGENTS_ORCH_MAX_RUNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if c.MaxConcurrentRuns > 0 {
		return c.MaxConcurrentRuns
	}
	return 4
}
