// Package toolexec implements the host-bound inline tools.
package toolexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Result struct {
	Output string `json:"output"`
	Error  bool   `json:"is_error"`
}

type Executor struct {
	root string
}

func New(root string) (*Executor, error) {
	if root == "" {
		return nil, errors.New("allowed workdir root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Executor{root: abs}, nil
}

func (e *Executor) Run(ctx context.Context, name string, args map[string]any) Result {
	switch name {
	case "read":
		return e.read(args)
	case "write":
		return e.write(args)
	case "edit":
		return e.edit(args)
	case "bash", "shell":
		return e.bash(ctx, args)
	case "ls":
		return e.ls(args)
	default:
		return Result{Output: "unknown tool: " + name, Error: true}
	}
}

func (e *Executor) read(args map[string]any) Result {
	path, err := e.resolve(requiredString(args, "path"))
	if err != nil {
		return errResult(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(err)
	}
	text := string(data)
	runes := []rune(text)
	offset, hasOffset, err := optionalInt(args, "offset")
	if err != nil {
		return errResult(err)
	}
	limit, hasLimit, err := optionalInt(args, "limit")
	if err != nil {
		return errResult(err)
	}
	if hasOffset {
		if offset < 0 || offset > len(runes) {
			return Result{Output: "offset out of range", Error: true}
		}
		runes = runes[offset:]
	}
	if hasLimit {
		if limit < 0 {
			return Result{Output: "limit out of range", Error: true}
		}
		if limit < len(runes) {
			runes = runes[:limit]
		}
	}
	return Result{Output: string(runes)}
}

func (e *Executor) write(args map[string]any) Result {
	path, err := e.resolve(requiredString(args, "path"))
	if err != nil {
		return errResult(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errResult(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprint(args["content"])), 0o644); err != nil {
		return errResult(err)
	}
	return Result{Output: "wrote " + path}
}

func (e *Executor) edit(args map[string]any) Result {
	path, err := e.resolve(requiredString(args, "path"))
	if err != nil {
		return errResult(err)
	}
	old := firstString(args, "old_string", "old")
	newText := firstString(args, "new_string", "new")
	if old == "" {
		return Result{Output: "old text is required", Error: true}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(err)
	}
	content := string(data)
	count := strings.Count(content, old)
	if count == 0 {
		return Result{Output: "old text not found", Error: true}
	}
	replaceAll, _ := optionalBool(args, "replace_all")
	if !replaceAll && count != 1 {
		return Result{Output: "old text is not unique", Error: true}
	}
	n := 1
	if replaceAll {
		n = -1
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, old, newText, n)), 0o644); err != nil {
		return errResult(err)
	}
	return Result{Output: "edited " + path}
}

func (e *Executor) bash(ctx context.Context, args map[string]any) Result {
	command := requiredString(args, "command")
	if command == "" {
		return Result{Output: "command is required", Error: true}
	}
	timeout := 120 * time.Second
	if v, ok, err := optionalFloat(args, "timeout"); err != nil {
		return errResult(err)
	} else if ok {
		timeout = time.Duration(v * float64(time.Second))
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(bashExecutable(), "-lc", command)
	cmd.Dir = e.root
	configureCommand(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return errResult(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	var err error
	select {
	case err = <-done:
	case <-cctx.Done():
		killProcessTree(cmd)
		select {
		case err = <-done:
		case <-time.After(250 * time.Millisecond):
			forceKillProcess(cmd)
			select {
			case err = <-done:
			case <-time.After(250 * time.Millisecond):
				return Result{Output: fmt.Sprintf("timeout after %gs", timeout.Seconds()), Error: true}
			}
		}
	}
	output := out.String()
	if cctx.Err() == context.DeadlineExceeded {
		return Result{Output: fmt.Sprintf("timeout after %gs", timeout.Seconds()), Error: true}
	}
	if err != nil {
		code := 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return Result{Output: fmt.Sprintf("exit_code=%d\n%s", code, output), Error: true}
	}
	return Result{Output: output}
}

func bashExecutable() string {
	if explicit := os.Getenv("AGENTOS_BASH"); explicit != "" {
		return explicit
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{
			`C:\Program Files\Git\bin\bash.exe`,
			`C:\Program Files\Git\usr\bin\bash.exe`,
		} {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	return "bash"
}

func (e *Executor) ls(args map[string]any) Result {
	raw := "."
	if v, ok := args["path"]; ok {
		raw = fmt.Sprint(v)
	}
	path, err := e.resolve(raw)
	if err != nil {
		return errResult(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return errResult(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return Result{Output: strings.Join(names, "\n")}
}

func (e *Executor) resolve(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("path is required")
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(e.root, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(e.root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes allowed root: %s", raw)
	}
	return abs, nil
}

func errResult(err error) Result {
	return Result{Output: err.Error(), Error: true}
}

func requiredString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func firstString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := args[key]; ok {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func optionalBool(args map[string]any, key string) (bool, bool) {
	v, ok := args[key]
	if !ok {
		return false, false
	}
	switch typed := v.(type) {
	case bool:
		return typed, true
	case string:
		return typed == "true", true
	default:
		return fmt.Sprint(v) == "true", true
	}
}

func optionalInt(args map[string]any, key string) (int, bool, error) {
	v, ok := args[key]
	if !ok {
		return 0, false, nil
	}
	switch typed := v.(type) {
	case int:
		return typed, true, nil
	case int64:
		return int(typed), true, nil
	case float64:
		return int(typed), true, nil
	case jsonNumber:
		i, err := strconv.Atoi(string(typed))
		return i, true, err
	default:
		i, err := strconv.Atoi(fmt.Sprint(v))
		return i, true, err
	}
}

func optionalFloat(args map[string]any, key string) (float64, bool, error) {
	v, ok := args[key]
	if !ok {
		return 0, false, nil
	}
	switch typed := v.(type) {
	case float64:
		return typed, true, nil
	case int:
		return float64(typed), true, nil
	default:
		f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f, true, err
	}
}

type jsonNumber string
