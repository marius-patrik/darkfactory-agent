package kubernetes

import (
	"strings"
	"testing"
)

func TestJobName(t *testing.T) {
	got := JobName("Run_ABC.123")
	if got != "agent-run-abc-123" {
		t.Fatalf("unexpected job name: %s", got)
	}
	if len(JobName(strings.Repeat("x", 100))) > 63 {
		t.Fatal("job name must fit Kubernetes DNS label length")
	}
}

func TestBuildJobManifest(t *testing.T) {
	manifest, err := BuildJobManifest(
		"agent-run-1",
		"agents/harness:latest",
		[]string{"agents", "run"},
		map[string]string{"AGENTS_RUN_ID": "run-1", "FOO": "bar"},
		map[string]string{"tenant": "qft"},
	)
	if err != nil {
		t.Fatalf("BuildJobManifest: %v", err)
	}
	text := string(manifest)
	for _, want := range []string{
		"kind: Job",
		"name: agent-run-1",
		"image: \"agents/harness:latest\"",
		"imagePullPolicy: IfNotPresent",
		"backoffLimit: 0",
		"activeDeadlineSeconds: 1200",
		"name: \"AGENTS_RUN_ID\"",
		"value: \"run-1\"",
		"agents.platform/run-id: \"run-1\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %q:\n%s", want, text)
		}
	}
}

func TestBuildJobManifestWithResourcesAndClusterSpread(t *testing.T) {
	manifest, err := BuildJobManifestWithOptions(
		"agent-run-1",
		"agents/harness:latest",
		[]string{"bash", "/app/run-task.sh"},
		map[string]string{"AGENTS_RUN_ID": "run-1"},
		map[string]string{"tenant": "qft"},
		JobOptions{
			CPURequest:          "2",
			MemoryRequest:       "8Gi",
			CPULimit:            "8",
			MemoryLimit:         "32Gi",
			GPULimit:            "1",
			TopologySpread:      true,
			AvoidDiskPressure:   true,
			DisallowedNodeNames: []string{"s001"},
		},
	)
	if err != nil {
		t.Fatalf("BuildJobManifestWithOptions: %v", err)
	}
	text := string(manifest)
	for _, want := range []string{
		"topologySpreadConstraints:",
		"topologyKey: kubernetes.io/hostname",
		"whenUnsatisfiable: ScheduleAnyway",
		"nodeAffinity:",
		"key: node.kubernetes.io/disk-pressure",
		"operator: DoesNotExist",
		"operator: NotIn",
		"- \"s001\"",
		"resources:",
		"requests:",
		"cpu: \"2\"",
		"memory: \"8Gi\"",
		"limits:",
		"cpu: \"8\"",
		"memory: \"32Gi\"",
		"nvidia.com/gpu: \"1\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %q:\n%s", want, text)
		}
	}
}

func TestBuildJobManifestMountsRuntimeRootForDurableEvidence(t *testing.T) {
	manifest, err := BuildJobManifest(
		"agent-run-1",
		"agents/harness:latest",
		[]string{"bash", "/app/run-task.sh"},
		map[string]string{
			"AGENTS_RUN_ID": "run-1",
			"AGENTS_ROOT":   "/home/patrik/agents",
		},
		map[string]string{"tenant": "qft"},
	)
	if err != nil {
		t.Fatalf("BuildJobManifest: %v", err)
	}
	text := string(manifest)
	for _, want := range []string{
		"volumeMounts:",
		"name: agents-root",
		"mountPath: \"/home/patrik/agents\"",
		"volumes:",
		"hostPath:",
		"path: \"/home/patrik/agents\"",
		"type: DirectoryOrCreate",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %q:\n%s", want, text)
		}
	}
}

func TestBuildJobManifestSkipsUnsafeRuntimeRootMount(t *testing.T) {
	for _, root := range []string{"", ".", "/", "relative/.agents"} {
		manifest, err := BuildJobManifest(
			"agent-run-1",
			"agents/harness:latest",
			nil,
			map[string]string{"AGENTS_RUN_ID": "run-1", "AGENTS_ROOT": root},
			nil,
		)
		if err != nil {
			t.Fatalf("BuildJobManifest(%q): %v", root, err)
		}
		text := string(manifest)
		if strings.Contains(text, "volumeMounts:") || strings.Contains(text, "hostPath:") {
			t.Fatalf("manifest mounted unsafe AGENTS_ROOT %q:\n%s", root, text)
		}
	}
}

func TestBuildJobManifestSanitizesLabelValues(t *testing.T) {
	manifest, err := BuildJobManifest(
		"agent-run-1",
		"agents/harness:latest",
		nil,
		map[string]string{"AGENTS_RUN_ID": "run-1"},
		map[string]string{"task": "Create Wiki Index Generator Script"},
	)
	if err != nil {
		t.Fatalf("BuildJobManifest: %v", err)
	}
	text := string(manifest)
	if strings.Contains(text, "Create Wiki Index Generator Script") {
		t.Fatalf("raw label value with spaces must be sanitized:\n%s", text)
	}
	if !strings.Contains(text, "task: \"Create-Wiki-Index-Generator-Script\"") {
		t.Fatalf("expected sanitized task label:\n%s", text)
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	cases := map[string]string{
		"Create Wiki Index":    "Create-Wiki-Index",
		"--leading.trailing--": "leading.trailing",
		"ok-value_1.2":         "ok-value_1.2",
	}
	for in, want := range cases {
		if got := sanitizeLabelValue(in); got != want {
			t.Fatalf("sanitizeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
	long := sanitizeLabelValue(strings.Repeat("a", 100))
	if len(long) != 63 {
		t.Fatalf("expected truncation to 63, got %d", len(long))
	}
}

func TestBuildJobManifestRequiresImage(t *testing.T) {
	_, err := BuildJobManifest("agent-run-1", "", nil, map[string]string{"AGENTS_RUN_ID": "run-1"}, nil)
	if err == nil {
		t.Fatal("expected missing image error")
	}
}

func TestIsNotFound(t *testing.T) {
	err := &kubectlError{"kubectl -n agents get job x: exit status 1: Error from server (NotFound): jobs.batch \"x\" not found"}
	if !isNotFound(err) {
		t.Fatal("expected Kubernetes NotFound error to be recognized")
	}
	if isNotFound(nil) {
		t.Fatal("nil error must not be NotFound")
	}
}

type kubectlError struct {
	msg string
}

func (e *kubectlError) Error() string {
	return e.msg
}
