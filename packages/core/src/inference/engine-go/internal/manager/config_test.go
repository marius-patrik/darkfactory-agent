package manager

import (
	"strings"
	"testing"
)

func TestDefaultConfigUsesStandardGatewayPort(t *testing.T) {
	c := DefaultConfig()
	if c.GatewayURL != "http://localhost:4000" {
		t.Fatalf("expected default gateway URL http://localhost:4000, got %q", c.GatewayURL)
	}
	if strings.Contains(c.GatewayURL, "14000") {
		t.Fatalf("default gateway URL must not contain phantom port 14000, got %q", c.GatewayURL)
	}
}

func TestResolveRunGatewayURL(t *testing.T) {
	// Falls back to GatewayURL when RunGatewayURL is unset.
	c := DefaultConfig()
	c.GatewayURL = "http://gateway:4000"
	if got := c.ResolveRunGatewayURL(); got != "http://gateway:4000" {
		t.Fatalf("expected fallback to GatewayURL, got %q", got)
	}
	// Overrides with a cluster-routable address for run pods.
	c.RunGatewayURL = "http://192.168.0.29:4000"
	if got := c.ResolveRunGatewayURL(); got != "http://192.168.0.29:4000" {
		t.Fatalf("expected RunGatewayURL override, got %q", got)
	}
}

func TestFromEnvSetsRunGatewayURL(t *testing.T) {
	t.Setenv("AGENTS_MANAGER_GATEWAY_URL", "http://gateway:4000")
	t.Setenv("AGENTS_MANAGER_RUN_GATEWAY_URL", "http://192.168.0.48:4000")
	c := FromEnv()
	if c.GatewayURL != "http://gateway:4000" {
		t.Fatalf("manager gateway URL should stay the compose endpoint, got %q", c.GatewayURL)
	}
	if c.ResolveRunGatewayURL() != "http://192.168.0.48:4000" {
		t.Fatalf("run gateway URL should be the node-routable endpoint, got %q", c.ResolveRunGatewayURL())
	}
}

func TestFromEnvAcceptsLegacyOrchPrefix(t *testing.T) {
	t.Setenv("AGENTS_ORCH_ADDR", ":18081")
	t.Setenv("AGENTS_ORCH_REPO_OWNER", "legacy-owner")
	t.Setenv("AGENTS_ORCH_REPO_NAME", "legacy-repo")
	t.Setenv("AGENTS_ORCH_POLL", "5s")
	t.Setenv("AGENTS_ORCH_DAEMON_URL", "http://daemon-a:8080, http://daemon-b:8080")
	t.Setenv("AGENTS_ORCH_GATEWAY_URL", "http://gateway:4000")
	t.Setenv("AGENTS_ORCH_DB", "/tmp/legacy-manager.db")
	t.Setenv("AGENTS_ORCH_IMAGE", "agents/harness:v3.0.42")
	t.Setenv("AGENTS_ORCH_BASE_BRANCH", "release")
	t.Setenv("AGENTS_ORCH_VERSION", "3.0.42")
	t.Setenv("AGENTS_ORCH_MAX_RUNS", "7")

	c := FromEnv()

	if c.ListenAddr != ":18081" {
		t.Fatalf("expected legacy listen addr, got %q", c.ListenAddr)
	}
	if c.RepoOwner != "legacy-owner" || c.RepoName != "legacy-repo" {
		t.Fatalf("expected legacy repo, got %s/%s", c.RepoOwner, c.RepoName)
	}
	if c.PollInterval != "5s" {
		t.Fatalf("expected legacy poll interval, got %q", c.PollInterval)
	}
	if c.DaemonURL != "http://daemon-a:8080" || len(c.DaemonURLs) != 2 || c.DaemonURLs[1] != "http://daemon-b:8080" {
		t.Fatalf("expected parsed legacy daemon urls, got %#v / %q", c.DaemonURLs, c.DaemonURL)
	}
	if c.GatewayURL != "http://gateway:4000" {
		t.Fatalf("expected legacy gateway url, got %q", c.GatewayURL)
	}
	if c.DBPath != "/tmp/legacy-manager.db" {
		t.Fatalf("expected legacy db path, got %q", c.DBPath)
	}
	if c.DefaultImage != "agents/harness:v3.0.42" {
		t.Fatalf("expected legacy image, got %q", c.DefaultImage)
	}
	if c.BaseBranch != "release" {
		t.Fatalf("expected legacy base branch, got %q", c.BaseBranch)
	}
	if c.Version != "3.0.42" {
		t.Fatalf("expected legacy version, got %q", c.Version)
	}
	if got := c.ResolveMaxConcurrentRuns(); got != 7 {
		t.Fatalf("expected legacy max runs, got %d", got)
	}
}

func TestFromEnvPrefersManagerPrefixOverLegacyOrchPrefix(t *testing.T) {
	t.Setenv("AGENTS_ORCH_ADDR", ":18081")
	t.Setenv("AGENTS_ORCH_IMAGE", "agents/harness:legacy")
	t.Setenv("AGENTS_ORCH_VERSION", "3.0.41")
	t.Setenv("AGENTS_ORCH_MAX_RUNS", "7")
	t.Setenv("AGENTS_MANAGER_ADDR", ":28081")
	t.Setenv("AGENTS_MANAGER_IMAGE", "agents/harness:manager")
	t.Setenv("AGENTS_MANAGER_VERSION", "3.0.42")
	t.Setenv("AGENTS_MANAGER_MAX_RUNS", "9")

	c := FromEnv()

	if c.ListenAddr != ":28081" {
		t.Fatalf("expected manager listen addr, got %q", c.ListenAddr)
	}
	if c.DefaultImage != "agents/harness:manager" {
		t.Fatalf("expected manager image, got %q", c.DefaultImage)
	}
	if c.Version != "3.0.42" {
		t.Fatalf("expected manager version, got %q", c.Version)
	}
	if got := c.ResolveMaxConcurrentRuns(); got != 9 {
		t.Fatalf("expected manager max runs, got %d", got)
	}
}
