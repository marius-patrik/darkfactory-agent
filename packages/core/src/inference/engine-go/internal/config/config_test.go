package config

import (
	"os"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.ListenAddr != ":8080" {
		t.Fatalf("expected :8080, got %s", c.ListenAddr)
	}
	if c.ConcurrencyCap != 4 {
		t.Fatalf("expected cap 4, got %d", c.ConcurrencyCap)
	}
	if c.DBPath != "agents_daemon.db" {
		t.Fatalf("expected agents_daemon.db, got %s", c.DBPath)
	}
	if c.Executor != "kubernetes" {
		t.Fatalf("expected kubernetes executor, got %s", c.Executor)
	}
	if c.KubernetesNamespace != "agents" {
		t.Fatalf("expected agents namespace, got %s", c.KubernetesNamespace)
	}
	if c.K8SCPURequest == "" || c.K8SMemoryRequest == "" || c.K8SCPULimit == "" || c.K8SMemoryLimit == "" {
		t.Fatalf("expected default CPU/memory requests and limits, got %#v", c)
	}
	if !c.K8STopologySpread || !c.K8SAvoidDiskPressure {
		t.Fatalf("expected topology spread and disk-pressure avoidance defaults, got %#v", c)
	}
	if c.K8SGPULimit != "1" {
		t.Fatalf("expected k8s gpu limit 1, got %s", c.K8SGPULimit)
	}
}

func TestFromEnv(t *testing.T) {
	os.Setenv("AGENTS_DAEMON_ADDR", ":9090")
	os.Setenv("AGENTS_DAEMON_CAP", "8")
	os.Setenv("AGENTS_DAEMON_DB", "/tmp/test.db")
	os.Setenv("AGENTS_K8S_CPU_REQUEST", "4")
	os.Setenv("AGENTS_K8S_MEMORY_REQUEST", "16Gi")
	os.Setenv("AGENTS_K8S_CPU_LIMIT", "12")
	os.Setenv("AGENTS_K8S_MEMORY_LIMIT", "48Gi")
	os.Setenv("AGENTS_K8S_GPU_LIMIT", "1")
	os.Setenv("AGENTS_K8S_TOPOLOGY_SPREAD", "0")
	os.Setenv("AGENTS_K8S_AVOID_DISK_PRESSURE", "0")
	os.Setenv("AGENTS_K8S_DISALLOWED_NODES", "s001")
	defer func() {
		os.Unsetenv("AGENTS_DAEMON_ADDR")
		os.Unsetenv("AGENTS_DAEMON_CAP")
		os.Unsetenv("AGENTS_DAEMON_DB")
		os.Unsetenv("AGENTS_K8S_CPU_REQUEST")
		os.Unsetenv("AGENTS_K8S_MEMORY_REQUEST")
		os.Unsetenv("AGENTS_K8S_CPU_LIMIT")
		os.Unsetenv("AGENTS_K8S_MEMORY_LIMIT")
		os.Unsetenv("AGENTS_K8S_GPU_LIMIT")
		os.Unsetenv("AGENTS_K8S_TOPOLOGY_SPREAD")
		os.Unsetenv("AGENTS_K8S_AVOID_DISK_PRESSURE")
		os.Unsetenv("AGENTS_K8S_DISALLOWED_NODES")
	}()

	c := FromEnv()
	if c.ListenAddr != ":9090" {
		t.Fatalf("expected :9090, got %s", c.ListenAddr)
	}
	if c.ConcurrencyCap != 8 {
		t.Fatalf("expected cap 8, got %d", c.ConcurrencyCap)
	}
	if c.DBPath != "/tmp/test.db" {
		t.Fatalf("expected /tmp/test.db, got %s", c.DBPath)
	}
	if c.K8SCPURequest != "4" || c.K8SMemoryRequest != "16Gi" || c.K8SCPULimit != "12" || c.K8SMemoryLimit != "48Gi" || c.K8SGPULimit != "1" {
		t.Fatalf("unexpected k8s resource config: %#v", c)
	}
	if c.K8STopologySpread || c.K8SAvoidDiskPressure || c.K8SDisallowedNodes != "s001" {
		t.Fatalf("unexpected k8s scheduling config: %#v", c)
	}
}

func TestFromFile(t *testing.T) {
	f, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString(`{"listen_addr": ":7070", "concurrency_cap": 2}`)
	f.Close()

	c, err := FromFile(f.Name())
	if err != nil {
		t.Fatalf("from file: %v", err)
	}
	if c.ListenAddr != ":7070" {
		t.Fatalf("expected :7070, got %s", c.ListenAddr)
	}
	if c.ConcurrencyCap != 2 {
		t.Fatalf("expected cap 2, got %d", c.ConcurrencyCap)
	}
}

func TestFromFile_Missing(t *testing.T) {
	c, err := FromFile("/nonexistent/config.json")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if c.ListenAddr != ":8080" {
		t.Fatalf("expected defaults for missing file")
	}
}
