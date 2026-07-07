// Package config loads daemon configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// C holds daemon configuration.
type C struct {
	ListenAddr     string `json:"listen_addr"`
	ConcurrencyCap int    `json:"concurrency_cap"`
	DBPath         string `json:"db_path"`
	NATSURL        string `json:"nats_url"`
	NATSTopic      string `json:"nats_topic"`
	Executor       string `json:"executor"`
	DockerEnabled  bool   `json:"docker_enabled"`
	Version        string `json:"version"`
	NodeID         string `json:"node_id"`
	// Kubernetes executor settings.
	KubernetesNamespace  string `json:"kubernetes_namespace,omitempty"`
	Kubeconfig           string `json:"kubeconfig,omitempty"`
	K8SCPURequest        string `json:"k8s_cpu_request,omitempty"`
	K8SMemoryRequest     string `json:"k8s_memory_request,omitempty"`
	K8SCPULimit          string `json:"k8s_cpu_limit,omitempty"`
	K8SMemoryLimit       string `json:"k8s_memory_limit,omitempty"`
	K8SGPULimit          string `json:"k8s_gpu_limit,omitempty"`
	K8STopologySpread    bool   `json:"k8s_topology_spread,omitempty"`
	K8SAvoidDiskPressure bool   `json:"k8s_avoid_disk_pressure,omitempty"`
	K8SDisallowedNodes   string `json:"k8s_disallowed_nodes,omitempty"`
	// GitHub Actions executor settings. Retained only for legacy/local bridge
	// compatibility; v3 execution uses Kubernetes Jobs.
	GitHubToken    string `json:"github_token,omitempty"`
	GitHubOwner    string `json:"github_owner,omitempty"`
	GitHubRepo     string `json:"github_repo,omitempty"`
	GitHubWorkflow string `json:"github_workflow,omitempty"`
	GitHubRef      string `json:"github_ref,omitempty"`
}

// Default returns sensible defaults.
func Default() C {
	return C{
		ListenAddr:           ":8080",
		ConcurrencyCap:       4,
		DBPath:               "agents_daemon.db",
		NATSURL:              os.Getenv("NATS_URL"),
		NATSTopic:            "agents.runs",
		Executor:             "kubernetes",
		DockerEnabled:        true,
		Version:              "3.0.0",
		NodeID:               os.Getenv("NODE_ID"),
		KubernetesNamespace:  "agents",
		K8SCPURequest:        "2",
		K8SMemoryRequest:     "8Gi",
		K8SCPULimit:          "8",
		K8SMemoryLimit:       "32Gi",
		K8SGPULimit:          "1",
		K8STopologySpread:    true,
		K8SAvoidDiskPressure: true,
	}
}

// FromEnv overlays environment variables onto defaults.
func FromEnv() C {
	c := Default()
	if v := os.Getenv("AGENTS_DAEMON_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("AGENTS_DAEMON_CAP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.ConcurrencyCap = n
		}
	}
	if v := os.Getenv("AGENTS_DAEMON_DB"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("NATS_URL"); v != "" {
		c.NATSURL = v
	}
	if v := os.Getenv("AGENTS_DAEMON_VERSION"); v != "" {
		c.Version = v
	}
	if v := os.Getenv("NODE_ID"); v != "" {
		c.NodeID = v
	}
	if v := os.Getenv("AGENTS_K8S_NAMESPACE"); v != "" {
		c.KubernetesNamespace = v
	}
	if v := os.Getenv("KUBECONFIG"); v != "" {
		c.Kubeconfig = v
	} else if v := os.Getenv("AGENTS_KUBECONFIG"); v != "" {
		c.Kubeconfig = v
	}
	if v := os.Getenv("AGENTS_K8S_CPU_REQUEST"); v != "" {
		c.K8SCPURequest = v
	}
	if v := os.Getenv("AGENTS_K8S_MEMORY_REQUEST"); v != "" {
		c.K8SMemoryRequest = v
	}
	if v := os.Getenv("AGENTS_K8S_CPU_LIMIT"); v != "" {
		c.K8SCPULimit = v
	}
	if v := os.Getenv("AGENTS_K8S_MEMORY_LIMIT"); v != "" {
		c.K8SMemoryLimit = v
	}
	if v := os.Getenv("AGENTS_K8S_GPU_LIMIT"); v != "" {
		c.K8SGPULimit = v
	}
	if v := os.Getenv("AGENTS_K8S_TOPOLOGY_SPREAD"); v != "" {
		c.K8STopologySpread = v != "0" && v != "false"
	}
	if v := os.Getenv("AGENTS_K8S_AVOID_DISK_PRESSURE"); v != "" {
		c.K8SAvoidDiskPressure = v != "0" && v != "false"
	}
	if v := os.Getenv("AGENTS_K8S_DISALLOWED_NODES"); v != "" {
		c.K8SDisallowedNodes = v
	}

	// Resilient Executor check
	if v := os.Getenv("AGENTS_DAEMON_EXECUTOR"); v != "" {
		c.Executor = v
	} else if v := os.Getenv("AGENTS_EXECUTOR"); v != "" {
		c.Executor = v
	} else if v := os.Getenv("EXECUTOR"); v != "" {
		c.Executor = v
	}

	// Resilient GitHub Token check
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		c.GitHubToken = v
	} else if v := os.Getenv("GH_TOKEN"); v != "" {
		c.GitHubToken = v
	}

	// Resilient GitHub Owner check
	if v := os.Getenv("GITHUB_OWNER"); v != "" {
		c.GitHubOwner = v
	} else if v := os.Getenv("GH_OWNER"); v != "" {
		c.GitHubOwner = v
	} else if v := os.Getenv("AGENTS_MANAGER_REPO_OWNER"); v != "" {
		c.GitHubOwner = v
	}

	// Resilient GitHub Repo check
	if v := os.Getenv("GITHUB_REPO"); v != "" {
		c.GitHubRepo = v
	} else if v := os.Getenv("GH_REPO"); v != "" {
		c.GitHubRepo = v
	} else if v := os.Getenv("AGENTS_MANAGER_REPO_NAME"); v != "" {
		c.GitHubRepo = v
	}

	// Resilient GitHub Ref check
	if v := os.Getenv("GITHUB_REF"); v != "" {
		c.GitHubRef = v
	} else if v := os.Getenv("GH_REF"); v != "" {
		c.GitHubRef = v
	} else if v := os.Getenv("AGENTS_DAEMON_REF"); v != "" {
		c.GitHubRef = v
	}

	return c
}

// FromFile loads JSON config from a path.
func FromFile(path string) (C, error) {
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
	return c, nil
}
