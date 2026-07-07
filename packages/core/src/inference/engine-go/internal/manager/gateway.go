package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Message is an OpenAI-format chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GatewayClient calls the LiteLLM gateway for chat completions.
type GatewayClient struct {
	baseURL string
	client  *http.Client
}

// NewGatewayClient creates a client pointing at the gateway.
func NewGatewayClient(baseURL string) *GatewayClient {
	return &GatewayClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// ChatCompletion sends a chat request and returns the assistant's content.
func (c *GatewayClient) ChatCompletion(ctx context.Context, modelRole string, messages []Message, allowCloud bool) (string, error) {
	payload := map[string]any{
		"model":    modelRole,
		"messages": messages,
	}
	if allowCloud {
		payload["allow_cloud"] = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

// DecomposePrompt returns the system + user messages for PRD decomposition.
func DecomposePrompt(prdTitle, prdBody string) []Message {
	system := `You are the Agents platform manager. Decompose the following PRD into a JSON array of sub-tasks.
Each sub-task must be an object with fields: "title" (string), "description" (string), "command" (array of strings), "env" (object of strings).
The command should run the agent harness in headless mode to complete the sub-task.
Respond with ONLY the JSON array, no markdown fences.`
	user := fmt.Sprintf("PRD Title: %s\n\nPRD Body:\n%s", prdTitle, prdBody)
	return []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
}

// SubTask is a single unit of work extracted from a PRD.
type SubTask struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Command     []string          `json:"command"`
	Env         map[string]string `json:"env"`
}

// ParseSubTasks parses the LLM response into sub-tasks.
func ParseSubTasks(raw string) ([]SubTask, error) {
	// Strip markdown fences if present.
	clean := raw
	if i := strings.Index(clean, "["); i != -1 {
		clean = clean[i:]
	}
	if i := strings.LastIndex(clean, "]"); i != -1 {
		clean = clean[:i+1]
	}
	var tasks []SubTask
	if err := json.Unmarshal([]byte(clean), &tasks); err != nil {
		return nil, fmt.Errorf("parse sub-tasks: %w", err)
	}
	return tasks, nil
}
