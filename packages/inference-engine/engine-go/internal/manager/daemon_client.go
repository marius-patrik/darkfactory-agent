package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

// DaemonClient submits runs to the manager daemon HTTP API.
type DaemonClient struct {
	mu        sync.Mutex
	baseURLs  []string
	leaderIdx int
	client    *http.Client
}

// NewDaemonClient creates a daemon API client.
func NewDaemonClient(baseURL string) *DaemonClient {
	return NewDaemonClientMulti([]string{baseURL})
}

// NewDaemonClientMulti creates a daemon API client for multiple daemon endpoints.
func NewDaemonClientMulti(urls []string) *DaemonClient {
	return &DaemonClient{
		baseURLs: normalizeDaemonURLs(urls),
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

// SubmitRun creates a run via POST /v1/runs.
func (c *DaemonClient) SubmitRun(ctx context.Context, req contracts.SubmitRunRequest) (*contracts.Run, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	baseURLs, start := c.endpoints()
	if len(baseURLs) == 0 {
		return nil, fmt.Errorf("no leader among daemon endpoints")
	}

	for offset := range baseURLs {
		idx := (start + offset) % len(baseURLs)
		r, err := http.NewRequestWithContext(ctx, "POST", baseURLs[idx]+"/v1/runs", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		r.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(r)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusServiceUnavailable {
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusAccepted {
			status := resp.StatusCode
			resp.Body.Close()
			return nil, fmt.Errorf("daemon status %d", status)
		}

		var run contracts.Run
		if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode response: %w", err)
		}
		resp.Body.Close()
		c.setLeaderIdx(idx)
		return &run, nil
	}

	return nil, fmt.Errorf("no leader among daemon endpoints")
}

// GetRun fetches a run by ID.
func (c *DaemonClient) GetRun(ctx context.Context, id string) (*contracts.Run, error) {
	baseURLs, start := c.endpoints()
	if len(baseURLs) == 0 {
		return nil, fmt.Errorf("daemon status %d", http.StatusNotFound)
	}

	var lastErr error
	for offset := range baseURLs {
		idx := (start + offset) % len(baseURLs)
		req, err := http.NewRequestWithContext(ctx, "GET", baseURLs[idx]+"/v1/runs/"+id, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("daemon status %d", resp.StatusCode)
			resp.Body.Close()
			continue
		}
		var run contracts.Run
		if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		c.setLeaderIdx(idx)
		return &run, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("daemon status %d", http.StatusNotFound)
}

// GetLogs fetches run logs by ID.
func (c *DaemonClient) GetLogs(ctx context.Context, id string) (string, error) {
	body, err := c.do(ctx, http.MethodGet, "/v1/runs/"+id+"/logs", nil, http.StatusOK)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// CancelRun requests cancellation for a run by ID.
func (c *DaemonClient) CancelRun(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodPost, "/v1/runs/"+id+"/cancel", nil, http.StatusOK)
	return err
}

func (c *DaemonClient) do(ctx context.Context, method, path string, body []byte, want int) ([]byte, error) {
	baseURLs, start := c.endpoints()
	if len(baseURLs) == 0 {
		return nil, fmt.Errorf("daemon status %d", http.StatusNotFound)
	}
	var lastErr error
	for offset := range baseURLs {
		idx := (start + offset) % len(baseURLs)
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURLs[idx]+path, reader)
		if err != nil {
			return nil, err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode != want {
			lastErr = fmt.Errorf("daemon status %d", resp.StatusCode)
			continue
		}
		c.setLeaderIdx(idx)
		return data, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("daemon status %d", http.StatusNotFound)
}

func (c *DaemonClient) endpoints() ([]string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	baseURLs := append([]string(nil), c.baseURLs...)
	if len(baseURLs) == 0 {
		return nil, 0
	}
	return baseURLs, c.leaderIdx % len(baseURLs)
}

func (c *DaemonClient) setLeaderIdx(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx >= 0 && idx < len(c.baseURLs) {
		c.leaderIdx = idx
	}
}

func normalizeDaemonURLs(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		out = append(out, strings.TrimRight(url, "/"))
	}
	return out
}

