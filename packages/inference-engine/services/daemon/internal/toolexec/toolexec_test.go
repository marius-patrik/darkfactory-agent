package toolexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestToolsOnTempDir(t *testing.T) {
	root := t.TempDir()
	exec, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if got := exec.Run(ctx, "write", map[string]any{"path": "a/b.txt", "content": "hello world"}); got.Error {
		t.Fatalf("write failed: %s", got.Output)
	}
	if got := exec.Run(ctx, "read", map[string]any{"path": "a/b.txt"}); got.Error || got.Output != "hello world" {
		t.Fatalf("read = %#v", got)
	}
	if got := exec.Run(ctx, "read", map[string]any{"path": "a/b.txt", "offset": 6, "limit": 5}); got.Error || got.Output != "world" {
		t.Fatalf("read slice = %#v", got)
	}
	if got := exec.Run(ctx, "edit", map[string]any{"path": "a/b.txt", "old_string": "world", "new_string": "rommie"}); got.Error {
		t.Fatalf("edit failed: %s", got.Output)
	}
	if got := exec.Run(ctx, "write", map[string]any{"path": "many.txt", "content": "x x x"}); got.Error {
		t.Fatalf("write many failed: %s", got.Output)
	}
	if got := exec.Run(ctx, "edit", map[string]any{"path": "many.txt", "old_string": "x", "new_string": "y"}); !got.Error || !strings.Contains(got.Output, "not unique") {
		t.Fatalf("expected unique-match error, got %#v", got)
	}
	if got := exec.Run(ctx, "edit", map[string]any{"path": "many.txt", "old_string": "x", "new_string": "y", "replace_all": true}); got.Error {
		t.Fatalf("replace_all failed: %s", got.Output)
	}
	if got := exec.Run(ctx, "read", map[string]any{"path": "many.txt"}); got.Output != "y y y" {
		t.Fatalf("replace_all content = %#v", got)
	}
	if got := exec.Run(ctx, "ls", map[string]any{"path": "."}); got.Error || !strings.Contains(got.Output, "a") || !strings.Contains(got.Output, "many.txt") {
		t.Fatalf("ls = %#v", got)
	}
	if got := exec.Run(ctx, "bash", map[string]any{"command": "echo ok"}); got.Error || got.Output != "ok\n" {
		t.Fatalf("bash ok = %#v", got)
	}
	if got := exec.Run(ctx, "bash", map[string]any{"command": "echo bad; exit 7"}); !got.Error || !strings.Contains(got.Output, "exit_code=7") {
		t.Fatalf("bash exit = %#v", got)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	exec, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := exec.Run(context.Background(), "read", map[string]any{"path": outside})
	if !got.Error || !strings.Contains(got.Output, "escapes allowed root") {
		t.Fatalf("expected traversal rejection, got %#v", got)
	}
}

func TestBashTimeout(t *testing.T) {
	exec, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Result, 1)
	start := time.Now()
	go func() {
		done <- exec.Run(context.Background(), "bash", map[string]any{"command": "sleep 2", "timeout": 0.1})
	}()

	got := awaitTimeoutResult(t, done)
	if !got.Error || !strings.Contains(got.Output, "timeout") {
		t.Fatalf("expected timeout, got %#v", got)
	}
	if elapsed := time.Since(start); elapsed > timeoutReturnLimit() {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

func TestBashTimeoutKillsProcessTree(t *testing.T) {
	root := t.TempDir()
	exec, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "timeout-child-leaked.txt")
	done := make(chan Result, 1)
	go func() {
		done <- exec.Run(context.Background(), "bash", map[string]any{
			"command": timeoutChildCommand(t, marker),
			"timeout": 0.5,
		})
	}()

	got := awaitTimeoutResult(t, done)
	if !got.Error || !strings.Contains(got.Output, "timeout") {
		t.Fatalf("expected timeout, got %#v", got)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("timeout did not kill child process")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestBashTimeoutChildProcess(t *testing.T) {
	if os.Getenv("TOOLEXEC_TIMEOUT_CHILD") != "1" {
		return
	}
	time.Sleep(time.Second)
	if err := os.WriteFile(os.Getenv("TOOLEXEC_TIMEOUT_MARKER"), []byte("leaked"), 0o644); err != nil {
		panic(err)
	}
	os.Exit(0)
}

func awaitTimeoutResult(t *testing.T, done <-chan Result) Result {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(timeoutReturnLimit()):
		t.Fatal("timeout command did not return")
		return Result{}
	}
}

func timeoutReturnLimit() time.Duration {
	if runtime.GOOS == "windows" {
		return 2 * time.Second
	}
	return time.Second
}

func timeoutChildCommand(t *testing.T, marker string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe = filepath.ToSlash(exe)
	return strings.Join([]string{
		"TOOLEXEC_TIMEOUT_CHILD=1",
		"TOOLEXEC_TIMEOUT_MARKER=" + shellQuote(marker),
		shellQuote(exe),
		"-test.run=TestBashTimeoutChildProcess",
		"--",
		"&",
		"wait",
	}, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
