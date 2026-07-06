package manager

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ghauth"
)

// Issue represents a GitHub issue.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Labels    []string  `json:"labels"`
	State     string    `json:"state"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// GitHubClient abstracts GitHub operations.
type GitHubClient interface {
	ListOpenIssues(ctx context.Context) ([]Issue, error)
	CreateBranch(ctx context.Context, branchName, from string) error
	CreateDraftPR(ctx context.Context, title, body, head, base string) (string, error)
	CreateIssue(ctx context.Context, title, body string, labels []string) (int, error)
	CreateOrUpdateFile(ctx context.Context, path, branch, message, content string) error
	AddComment(ctx context.Context, issueNumber int, body string) error
	AddLabels(ctx context.Context, issueNumber int, labels []string) error
	GetIssueLabels(ctx context.Context, issueNumber int) ([]string, error)
}

// GHRESTClient uses direct GitHub REST API calls via net/http.
type GHRESTClient struct {
	owner         string
	repo          string
	tokenProvider ghauth.TokenProvider
	client        *http.Client
}

// NewGHRESTClient creates a REST-based GitHub client for repo "owner/name".
func NewGHRESTClient(repo string, tokenProvider ghauth.TokenProvider) *GHRESTClient {
	parts := strings.SplitN(repo, "/", 2)
	owner, name := parts[0], ""
	if len(parts) > 1 {
		name = parts[1]
	}
	// Hardened transport: bound the connect, TLS, and (critically) response-header
	// waits per request, and recycle idle connections quickly. Without these, a
	// network path change (e.g. a route flap) leaves pooled keep-alive connections
	// half-open; reused requests then hang until the overall client Timeout on
	// every tick, wedging the leader's issue poll until a restart. ResponseHeaderTimeout
	// fails such a request fast and discards the dead connection; the short
	// IdleConnTimeout drains stale connections from the pool.
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 25 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &GHRESTClient{
		owner:         owner,
		repo:          name,
		tokenProvider: tokenProvider,
		client:        &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}
}

func (c *GHRESTClient) authHeader(ctx context.Context) (string, error) {
	if c.tokenProvider != nil {
		tok, err := c.tokenProvider.GetToken()
		if err != nil {
			return "", fmt.Errorf("get token: %w", err)
		}
		return "Bearer " + tok, nil
	}
	return "", nil
}

func (c *GHRESTClient) doJSON(ctx context.Context, method, url string, payload, dst any) error {
	auth, err := c.authHeader(ctx)
	if err != nil {
		return err
	}

	var bodyReader *bytes.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errBody strings.Builder
		// Read up to 4KB of error body for diagnostics
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		errBody.Write(buf[:n])
		return fmt.Errorf("github api %s %s -> %s: %s", method, url, resp.Status, errBody.String())
	}

	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// ListOpenIssues fetches open issues via GET /repos/{owner}/{repo}/issues.
func (c *GHRESTClient) ListOpenIssues(ctx context.Context) ([]Issue, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues?state=open&per_page=100", c.owner, c.repo)
	auth, err := c.authHeader(ctx)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody strings.Builder
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		errBody.Write(buf[:n])
		return nil, fmt.Errorf("list issues failed: %s: %s", resp.Status, errBody.String())
	}

	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		State       string    `json:"state"`
		HTMLURL     string    `json:"html_url"`
		CreatedAt   string    `json:"created_at"`
		PullRequest *struct{} `json:"pull_request,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("unmarshal issues: %w", err)
	}

	var issues []Issue
	for _, r := range raw {
		// Skip pull requests; the issues endpoint returns both.
		if r.PullRequest != nil {
			continue
		}
		labels := make([]string, 0, len(r.Labels))
		for _, l := range r.Labels {
			labels = append(labels, l.Name)
		}
		createdAt, _ := time.Parse(time.RFC3339, r.CreatedAt)
		issues = append(issues, Issue{
			Number:    r.Number,
			Title:     r.Title,
			Body:      r.Body,
			Labels:    labels,
			State:     r.State,
			URL:       r.HTMLURL,
			CreatedAt: createdAt,
		})
	}
	return issues, nil
}

// CreateBranch creates a branch from a base ref via the Git ref API.
func (c *GHRESTClient) CreateBranch(ctx context.Context, branchName, from string) error {
	sha, err := c.getRefSHA(ctx, from)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/refs", c.owner, c.repo)
	payload := map[string]string{
		"ref": "refs/heads/" + branchName,
		"sha": sha,
	}
	err = c.doJSON(ctx, http.MethodPost, url, payload, nil)
	if err != nil {
		if strings.Contains(err.Error(), "Reference already exists") || strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return fmt.Errorf("create branch %s: %w", branchName, err)
	}
	return nil
}

func (c *GHRESTClient) getRefSHA(ctx context.Context, ref string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/heads/%s", c.owner, c.repo, ref)
	var result struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.doJSON(ctx, http.MethodGet, url, nil, &result); err != nil {
		return "", fmt.Errorf("get ref %s: %w", ref, err)
	}
	return result.Object.SHA, nil
}

// CreateDraftPR opens a draft pull request.
func (c *GHRESTClient) CreateDraftPR(ctx context.Context, title, body, head, base string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", c.owner, c.repo)
	payload := map[string]any{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
		"draft": true,
	}
	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &result); err != nil {
		return "", fmt.Errorf("create draft PR: %w", err)
	}
	return result.HTMLURL, nil
}

// CreateIssue opens a new issue and returns its number.
func (c *GHRESTClient) CreateIssue(ctx context.Context, title, body string, labels []string) (int, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues", c.owner, c.repo)
	payload := map[string]any{
		"title":  title,
		"body":   body,
		"labels": labels,
	}
	var result struct {
		Number int `json:"number"`
	}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &result); err != nil {
		return 0, fmt.Errorf("create issue: %w", err)
	}
	return result.Number, nil
}

// CreateOrUpdateFile writes a UTF-8 text file to a branch using the Contents API.
func (c *GHRESTClient) CreateOrUpdateFile(ctx context.Context, path, branch, message, content string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", c.owner, c.repo, path)
	var existing struct {
		SHA string `json:"sha"`
	}
	getURL := url + "?ref=" + branch
	if err := c.doJSON(ctx, http.MethodGet, getURL, nil, &existing); err != nil {
		if !strings.Contains(err.Error(), "404") {
			return fmt.Errorf("get file %s: %w", path, err)
		}
	}
	payload := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
	}
	if existing.SHA != "" {
		payload["sha"] = existing.SHA
	}
	if err := c.doJSON(ctx, http.MethodPut, url, payload, nil); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

// AddComment posts a comment on an issue.
func (c *GHRESTClient) AddComment(ctx context.Context, issueNumber int, body string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", c.owner, c.repo, issueNumber)
	payload := map[string]string{"body": body}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, nil); err != nil {
		return fmt.Errorf("add comment: %w", err)
	}
	return nil
}

// AddLabels adds labels to an issue.
func (c *GHRESTClient) AddLabels(ctx context.Context, issueNumber int, labels []string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/labels", c.owner, c.repo, issueNumber)
	payload := map[string][]string{"labels": labels}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, nil); err != nil {
		return fmt.Errorf("add labels: %w", err)
	}
	return nil
}

// GetIssueLabels returns labels for an issue or pull request issue number.
func (c *GHRESTClient) GetIssueLabels(ctx context.Context, issueNumber int) ([]string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d", c.owner, c.repo, issueNumber)
	var result struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := c.doJSON(ctx, http.MethodGet, url, nil, &result); err != nil {
		return nil, fmt.Errorf("get issue labels: %w", err)
	}
	labels := make([]string, 0, len(result.Labels))
	for _, label := range result.Labels {
		labels = append(labels, label.Name)
	}
	return labels, nil
}

