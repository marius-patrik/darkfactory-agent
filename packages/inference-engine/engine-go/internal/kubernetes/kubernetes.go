// Package kubernetes dispatches agent runs as Kubernetes Jobs via kubectl.
package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Config controls the Kubernetes Job runner.
type Config struct {
	Namespace             string
	Kubeconfig            string
	Kubectl               string
	CPURequest            string
	MemoryRequest         string
	CPULimit              string
	MemoryLimit           string
	GPULimit              string
	TopologySpread        bool
	AvoidDiskPressure     bool
	DisallowedNodeNames   []string
	PreferredNodeSelector map[string]string
}

// Runner starts and tracks one Kubernetes Job per agent run.
type Runner struct {
	cfg Config
	log *slog.Logger
}

// NewRunner creates a Kubernetes Job runner.
func NewRunner(cfg Config, log *slog.Logger) *Runner {
	if cfg.Namespace == "" {
		cfg.Namespace = "agents"
	}
	if cfg.Kubectl == "" {
		cfg.Kubectl = "kubectl"
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{cfg: cfg, log: log.With("executor", "kubernetes")}
}

// Start creates a Kubernetes Job and returns its name.
func (r *Runner) Start(ctx context.Context, image string, cmd []string, env, labels map[string]string) (string, error) {
	runID := env["AGENTS_RUN_ID"]
	if runID == "" {
		return "", fmt.Errorf("AGENTS_RUN_ID is required")
	}
	name := JobName(runID)
	env = normalizeMap(env)
	env["AGENTS_K8S_NAMESPACE"] = r.cfg.Namespace
	env["AGENTS_K8S_JOB_NAME"] = name
	env["AGENTS_K8S_CONTAINER_NAME"] = "agent"
	env["AGENTS_LOG_REF"] = fmt.Sprintf("k8s://%s/jobs/%s", r.cfg.Namespace, name)
	manifest, err := BuildJobManifestWithOptions(name, image, cmd, env, labels, r.cfg.JobOptions())
	if err != nil {
		return "", err
	}
	if _, err := r.kubectl(ctx, "create", "namespace", r.cfg.Namespace); err != nil {
		r.log.Debug("namespace create skipped", "namespace", r.cfg.Namespace, "err", err)
	}
	_, err = r.kubectlInput(ctx, manifest, "-n", r.cfg.Namespace, "apply", "-f", "-")
	if err != nil {
		return "", err
	}
	return name, nil
}

// Stop deletes the Job. Kubernetes terminates owned pods.
func (r *Runner) Stop(ctx context.Context, id string) error {
	_, err := r.kubectl(ctx, "-n", r.cfg.Namespace, "delete", "job", id, "--ignore-not-found=true")
	return err
}

// Remove deletes the Job after reconciliation.
func (r *Runner) Remove(ctx context.Context, id string) error {
	return r.Stop(ctx, id)
}

// IsRunning reports whether the Job has not reached a terminal condition.
func (r *Runner) IsRunning(ctx context.Context, id string) (bool, error) {
	out, err := r.kubectl(ctx, "-n", r.cfg.Namespace, "get", "job", id, "-o", "json")
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	var job struct {
		Status struct {
			Succeeded int `json:"succeeded"`
			Failed    int `json:"failed"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &job); err != nil {
		return false, err
	}
	return job.Status.Succeeded == 0 && job.Status.Failed == 0, nil
}

// ExitCode returns 0 for succeeded Jobs and 1 otherwise.
func (r *Runner) ExitCode(ctx context.Context, id string) (int, error) {
	out, err := r.kubectl(ctx, "-n", r.cfg.Namespace, "get", "job", id, "-o", "json")
	if err != nil {
		if isNotFound(err) {
			return 1, nil
		}
		return 1, err
	}
	var job struct {
		Status struct {
			Succeeded int `json:"succeeded"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &job); err != nil {
		return 1, err
	}
	if job.Status.Succeeded > 0 {
		return 0, nil
	}
	return 1, nil
}

// Logs returns all pod logs owned by the Job.
func (r *Runner) Logs(ctx context.Context, id string) (string, error) {
	out, err := r.kubectl(ctx, "-n", r.cfg.Namespace, "logs", "job/"+id, "--all-containers=true", "--tail=-1")
	return string(out), err
}

// URL returns a stable Kubernetes object reference.
func (r *Runner) URL(ctx context.Context, id string) (string, error) {
	return fmt.Sprintf("k8s://%s/jobs/%s", r.cfg.Namespace, id), nil
}

// Close is a no-op for the kubectl-backed runner.
func (r *Runner) Close() error { return nil }

func (r *Runner) kubectl(ctx context.Context, args ...string) ([]byte, error) {
	return r.kubectlInput(ctx, nil, args...)
}

func (r *Runner) kubectlInput(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	if r.cfg.Kubeconfig != "" {
		args = append([]string{"--kubeconfig", r.cfg.Kubeconfig}, args...)
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, r.cfg.Kubectl, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "(NotFound)") || strings.Contains(msg, "not found")
}

var unsafeDNS = regexp.MustCompile(`[^a-z0-9-]+`)

// JobName returns a DNS-label-compatible Job name.
func JobName(runID string) string {
	name := strings.ToLower(runID)
	name = unsafeDNS.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "run"
	}
	if len(name) > 52 {
		name = name[:52]
		name = strings.TrimRight(name, "-")
	}
	return "agent-" + name
}

// JobOptions controls scheduling and resource policy for agent Jobs.
type JobOptions struct {
	CPURequest            string
	MemoryRequest         string
	CPULimit              string
	MemoryLimit           string
	GPULimit              string
	TopologySpread        bool
	AvoidDiskPressure     bool
	DisallowedNodeNames   []string
	PreferredNodeSelector map[string]string
}

func (cfg Config) JobOptions() JobOptions {
	return JobOptions{
		CPURequest:            cfg.CPURequest,
		MemoryRequest:         cfg.MemoryRequest,
		CPULimit:              cfg.CPULimit,
		MemoryLimit:           cfg.MemoryLimit,
		GPULimit:              cfg.GPULimit,
		TopologySpread:        cfg.TopologySpread,
		AvoidDiskPressure:     cfg.AvoidDiskPressure,
		DisallowedNodeNames:   cfg.DisallowedNodeNames,
		PreferredNodeSelector: cfg.PreferredNodeSelector,
	}
}

// BuildJobManifest builds the Kubernetes Job manifest consumed by kubectl apply.
func BuildJobManifest(name, image string, cmd []string, env, labels map[string]string) ([]byte, error) {
	return BuildJobManifestWithOptions(name, image, cmd, env, labels, JobOptions{})
}

// BuildJobManifestWithOptions builds the Kubernetes Job manifest with explicit
// resource and scheduling policy.
func BuildJobManifestWithOptions(name, image string, cmd []string, env, labels map[string]string, opts JobOptions) ([]byte, error) {
	if image == "" {
		return nil, fmt.Errorf("image is required")
	}
	labels = normalizeMap(labels)
	for k, v := range labels {
		labels[k] = sanitizeLabelValue(v)
	}
	labels["app.kubernetes.io/name"] = "agents-run"
	labels["agents.platform/run-id"] = sanitizeLabelValue(env["AGENTS_RUN_ID"])
	env = normalizeMap(env)
	agentsRootMount := hostPathMount(env["AGENTS_ROOT"])

	var b strings.Builder
	b.WriteString("apiVersion: batch/v1\nkind: Job\nmetadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", name)
	b.WriteString("  labels:\n")
	writeMap(&b, labels, 4)
	b.WriteString("spec:\n  backoffLimit: 0\n  activeDeadlineSeconds: 1200\n  template:\n    metadata:\n      labels:\n")
	writeMap(&b, labels, 8)
	b.WriteString("    spec:\n")
	if opts.TopologySpread {
		b.WriteString("      topologySpreadConstraints:\n")
		b.WriteString("        - maxSkew: 1\n")
		b.WriteString("          topologyKey: kubernetes.io/hostname\n")
		b.WriteString("          whenUnsatisfiable: ScheduleAnyway\n")
		b.WriteString("          labelSelector:\n")
		b.WriteString("            matchLabels:\n")
		fmt.Fprintf(&b, "              app.kubernetes.io/name: %q\n", labels["app.kubernetes.io/name"])
	}
	if opts.AvoidDiskPressure || len(opts.DisallowedNodeNames) > 0 || len(opts.PreferredNodeSelector) > 0 {
		b.WriteString("      affinity:\n")
		b.WriteString("        nodeAffinity:\n")
		if opts.AvoidDiskPressure || len(opts.DisallowedNodeNames) > 0 {
			b.WriteString("          requiredDuringSchedulingIgnoredDuringExecution:\n")
			b.WriteString("            nodeSelectorTerms:\n")
			b.WriteString("              - matchExpressions:\n")
			if opts.AvoidDiskPressure {
				b.WriteString("                  - key: node.kubernetes.io/disk-pressure\n")
				b.WriteString("                    operator: DoesNotExist\n")
			}
			for _, node := range opts.DisallowedNodeNames {
				if node == "" {
					continue
				}
				b.WriteString("                  - key: kubernetes.io/hostname\n")
				b.WriteString("                    operator: NotIn\n")
				b.WriteString("                    values:\n")
				fmt.Fprintf(&b, "                      - %q\n", node)
			}
		}
		if len(opts.PreferredNodeSelector) > 0 {
			b.WriteString("          preferredDuringSchedulingIgnoredDuringExecution:\n")
			b.WriteString("            - weight: 50\n")
			b.WriteString("              preference:\n")
			b.WriteString("                matchExpressions:\n")
			for _, key := range sortedKeys(opts.PreferredNodeSelector) {
				b.WriteString("                  - key: " + key + "\n")
				b.WriteString("                    operator: In\n")
				b.WriteString("                    values:\n")
				fmt.Fprintf(&b, "                      - %q\n", opts.PreferredNodeSelector[key])
			}
		}
	}
	b.WriteString("      restartPolicy: Never\n      containers:\n        - name: agent\n")
	fmt.Fprintf(&b, "          image: %q\n", image)
	b.WriteString("          imagePullPolicy: IfNotPresent\n")
	writeResources(&b, opts, 10)
	if len(cmd) > 0 {
		b.WriteString("          command:\n")
		for _, item := range cmd {
			fmt.Fprintf(&b, "            - %q\n", item)
		}
	}
	if len(env) > 0 {
		b.WriteString("          env:\n")
		keys := sortedKeys(env)
		for _, key := range keys {
			fmt.Fprintf(&b, "            - name: %q\n              value: %q\n", key, env[key])
		}
	}
	if agentsRootMount != "" {
		b.WriteString("          volumeMounts:\n")
		b.WriteString("            - name: agents-root\n")
		fmt.Fprintf(&b, "              mountPath: %q\n", agentsRootMount)
	}
	if agentsRootMount != "" {
		b.WriteString("      volumes:\n")
		b.WriteString("        - name: agents-root\n")
		b.WriteString("          hostPath:\n")
		fmt.Fprintf(&b, "            path: %q\n", agentsRootMount)
		b.WriteString("            type: DirectoryOrCreate\n")
	}
	return []byte(b.String()), nil
}

func hostPathMount(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") {
		return ""
	}
	clean := pathpkg.Clean(path)
	if clean == "/" || clean == "." {
		return ""
	}
	return clean
}

func writeResources(b *strings.Builder, opts JobOptions, indent int) {
	requests := map[string]string{}
	limits := map[string]string{}
	if opts.CPURequest != "" {
		requests["cpu"] = opts.CPURequest
	}
	if opts.MemoryRequest != "" {
		requests["memory"] = opts.MemoryRequest
	}
	if opts.CPULimit != "" {
		limits["cpu"] = opts.CPULimit
	}
	if opts.MemoryLimit != "" {
		limits["memory"] = opts.MemoryLimit
	}
	if opts.GPULimit != "" && opts.GPULimit != "0" {
		limits["nvidia.com/gpu"] = opts.GPULimit
	}
	if len(requests) == 0 && len(limits) == 0 {
		return
	}
	pad := strings.Repeat(" ", indent)
	b.WriteString(pad + "resources:\n")
	if len(requests) > 0 {
		b.WriteString(pad + "  requests:\n")
		writeMap(b, requests, indent+4)
	}
	if len(limits) > 0 {
		b.WriteString(pad + "  limits:\n")
		writeMap(b, limits, indent+4)
	}
}

func normalizeMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func writeMap(b *strings.Builder, values map[string]string, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, key := range sortedKeys(values) {
		fmt.Fprintf(b, "%s%s: %q\n", pad, key, values[key])
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var unsafeLabelValue = regexp.MustCompile(`[^A-Za-z0-9-_.]+`)

// sanitizeLabelValue coerces s into a valid Kubernetes label value:
// alphanumerics plus -_. , beginning and ending alphanumeric, max 63 chars.
func sanitizeLabelValue(s string) string {
	s = unsafeLabelValue.ReplaceAllString(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.Trim(s, "-_.")
}
