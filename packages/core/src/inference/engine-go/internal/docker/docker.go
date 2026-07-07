// Package docker wraps the Docker SDK for container lifecycle ops.
package docker

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// Interface is the container runtime contract.
type Interface interface {
	Ping(ctx context.Context) error
	Start(ctx context.Context, imageName string, cmd []string, env, labels map[string]string) (string, error)
	Stop(ctx context.Context, containerID string) error
	Remove(ctx context.Context, containerID string) error
	IsRunning(ctx context.Context, containerID string) (bool, error)
	ExitCode(ctx context.Context, containerID string) (int, error)
	Logs(ctx context.Context, containerID string) (string, error)
	Close() error
}

// Manager executes container lifecycle operations.
type Manager struct {
	cli *client.Client
}

// NewManager creates a Manager talking to the local Docker daemon.
func NewManager() (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Manager{cli: cli}, nil
}

// Close closes the Docker client.
func (m *Manager) Close() error { return m.cli.Close() }

// Ping checks Docker connectivity.
func (m *Manager) Ping(ctx context.Context) error {
	_, err := m.cli.Ping(ctx)
	return err
}

// Start creates and starts a container, returning its ID.
func (m *Manager) Start(ctx context.Context, imageName string, cmd []string, env, labels map[string]string) (string, error) {
	// Ensure image is present; pull if not.
	_, _, err := m.cli.ImageInspectWithRaw(ctx, imageName)
	if err != nil {
		pullR, err := m.cli.ImagePull(ctx, imageName, image.PullOptions{})
		if err != nil {
			return "", fmt.Errorf("image pull: %w", err)
		}
		_, _ = io.Copy(io.Discard, pullR)
		_ = pullR.Close()
	}

	envSlice := make([]string, 0, len(env))
	for k, v := range env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	if labels == nil {
		labels = map[string]string{}
	}
	labels["agents.managed"] = "true"

	resp, err := m.cli.ContainerCreate(ctx,
		&container.Config{
			Image:  imageName,
			Cmd:    cmd,
			Env:    envSlice,
			Labels: labels,
		},
		&container.HostConfig{}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("container create: %w", err)
	}
	if err := m.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("container start: %w", err)
	}
	return resp.ID, nil
}

// Stop stops a container (SIGTERM then SIGKILL after timeout).
func (m *Manager) Stop(ctx context.Context, containerID string) error {
	if containerID == "" {
		return nil
	}
	err := m.cli.ContainerStop(ctx, containerID, container.StopOptions{})
	if err != nil && !client.IsErrNotFound(err) {
		return err
	}
	return nil
}

// Remove removes a container.
func (m *Manager) Remove(ctx context.Context, containerID string) error {
	if containerID == "" {
		return nil
	}
	err := m.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	if err != nil && !client.IsErrNotFound(err) {
		return err
	}
	return nil
}

// IsRunning returns true if the container is in the running state.
func (m *Manager) IsRunning(ctx context.Context, containerID string) (bool, error) {
	if containerID == "" {
		return false, nil
	}
	info, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return info.State.Running, nil
}

// ExitCode returns the container's exit code.
func (m *Manager) ExitCode(ctx context.Context, containerID string) (int, error) {
	if containerID == "" {
		return -1, nil
	}
	info, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return -1, nil
		}
		return -1, err
	}
	return info.State.ExitCode, nil
}

// URL returns an empty string for Docker runs.
func (m *Manager) URL(ctx context.Context, containerID string) (string, error) {
	return "", nil
}

// Logs returns the combined stdout+stderr of a container (last 10KB).
func (m *Manager) Logs(ctx context.Context, containerID string) (string, error) {
	if containerID == "" {
		return "", nil
	}
	out, err := m.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "10000",
	})
	if err != nil {
		if client.IsErrNotFound(err) {
			return "", nil
		}
		return "", err
	}
	defer out.Close()
	b, err := io.ReadAll(out)
	if err != nil {
		return "", err
	}
	// Docker multiplexes logs with 8-byte headers; best-effort strip.
	return stripDockerLogHeaders(string(b)), nil
}

// stripDockerLogHeaders removes Docker's binary stream headers best-effort.
func stripDockerLogHeaders(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); {
		if i+8 > len(s) {
			sb.WriteString(s[i:])
			break
		}
		// header: stream type (1) + 3 padding + 4 bytes big-endian size
		size := int(uint32(s[i+4])<<24 | uint32(s[i+5])<<16 | uint32(s[i+6])<<8 | uint32(s[i+7]))
		if size < 0 || size > len(s) || i+8+size > len(s) {
			sb.WriteString(s[i:])
			break
		}
		// Sanity check: if size is absurdly large, assume plain text
		if size > 1024*1024 {
			sb.WriteString(s[i:])
			break
		}
		sb.WriteString(s[i+8 : i+8+size])
		i += 8 + size
	}
	return sb.String()
}
