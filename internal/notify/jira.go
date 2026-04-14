package notify

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// JiraConfig holds the connection details for Jira REST API.
type JiraConfig struct {
	BaseURL string // e.g. https://yourorg.atlassian.net
	Email   string // e.g. bot@yourorg.com
	Token   string // API token (supports ${ENV_VAR} references)
}

// ResolveToken expands environment variable references in the token
// (e.g. "${JIRA_API_TOKEN}" → the actual value).
func (c *JiraConfig) ResolveToken() string {
	token := c.Token
	if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") {
		envVar := token[2 : len(token)-1]
		if val := os.Getenv(envVar); val != "" {
			return val
		}
	}
	return token
}

// JiraComment posts a comment on a Jira issue summarizing the agent run.
// It returns nil if cfg is nil or baseURL is empty (notifications disabled).
func JiraComment(cfg *JiraConfig, issueKey string, ev Event) error {
	if cfg == nil || cfg.BaseURL == "" || issueKey == "" {
		return nil
	}

	body := formatJiraComment(ev)
	return postJiraComment(cfg, issueKey, body)
}

func postJiraComment(cfg *JiraConfig, issueKey, body string) error {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment",
		strings.TrimRight(cfg.BaseURL, "/"), issueKey)

	// Jira Cloud uses Atlassian Document Format (ADF) for comments.
	payload := map[string]any{
		"body": map[string]any{
			"version": 1,
			"type":    "doc",
			"content": []map[string]any{
				{
					"type": "paragraph",
					"content": []map[string]any{
						{
							"type": "text",
							"text": body,
						},
					},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/jira: marshal: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("notify/jira: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Basic auth: email:token
	token := cfg.ResolveToken()
	auth := base64.StdEncoding.EncodeToString([]byte(cfg.Email + ":" + token))
	req.Header.Set("Authorization", "Basic "+auth)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("notify/jira: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify/jira: status %d on %s", resp.StatusCode, issueKey)
	}
	return nil
}

func formatJiraComment(ev Event) string {
	var sb strings.Builder

	switch ev.Type {
	case "start":
		fmt.Fprintf(&sb, "[procuracy] %s started working\n", ev.Contractor)
		fmt.Fprintf(&sb, "Engine: %s | Model: %s | Budget: $%.2f/task",
			ev.Engine, ev.Model, ev.Budget)

	case "complete":
		fmt.Fprintf(&sb, "[procuracy] %s completed\n", ev.Contractor)
		fmt.Fprintf(&sb, "Cost: $%.4f | Turns: %d | Duration: %s\n",
			ev.Cost, ev.Turns, formatDuration(ev.DurationMS))
		fmt.Fprintf(&sb, "Audit: %d entries verified", ev.AuditCount)

	case "fail":
		fmt.Fprintf(&sb, "[procuracy] %s failed\n", ev.Contractor)
		fmt.Fprintf(&sb, "Error: %s\n", ev.Error)
		fmt.Fprintf(&sb, "Cost: $%.4f | Duration: %s",
			ev.Cost, formatDuration(ev.DurationMS))

	case "cost_blocked":
		fmt.Fprintf(&sb, "[procuracy] %s hit cost limit\n", ev.Contractor)
		fmt.Fprintf(&sb, "Budget: $%.2f/task | %s", ev.Budget, ev.Error)
	}

	return sb.String()
}
