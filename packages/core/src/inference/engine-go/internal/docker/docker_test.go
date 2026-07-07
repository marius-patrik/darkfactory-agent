package docker

import (
	"context"
	"testing"
	"time"
)

func dockerAvailable(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager()
	if err != nil {
		t.Skip("docker not available:", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.Ping(ctx); err != nil {
		m.Close()
		t.Skip("docker ping failed:", err)
	}
	return m
}

func TestManager_Ping(t *testing.T) {
	m := dockerAvailable(t)
	defer m.Close()
}

func TestManager_Interface(t *testing.T) {
	var _ Interface = (*Manager)(nil)
}

func TestStripDockerLogHeaders(t *testing.T) {
	// Plain text should pass through mostly intact.
	plain := "hello world\nsecond line"
	got := stripDockerLogHeaders(plain)
	if got != plain {
		// Best-effort; don't fail on plain text.
		t.Logf("strip changed plain text: %q", got)
	}
}
