// Package jira provides a minimal Jira Cloud REST API client for
// procuracy's watch command. It handles ticket querying, status
// transitions, and comment posting — nothing else.
//
// This is intentionally not a full Jira SDK. It covers the three
// operations procuracy needs: find tickets assigned to an agent,
// move them through statuses, and post result comments.
package jira

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config holds the Jira connection details.
type Config struct {
	BaseURL string // e.g. https://yourorg.atlassian.net
	Email   string // e.g. bot@yourorg.com
	Token   string // API token (supports ${ENV_VAR} references)
}

// ResolveToken expands environment variable references.
func (c *Config) ResolveToken() string {
	token := c.Token
	if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") {
		if val := os.Getenv(token[2 : len(token)-1]); val != "" {
			return val
		}
	}
	return token
}

func (c *Config) authHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString(
		[]byte(c.Email+":"+c.ResolveToken()))
}

// Issue is a minimal representation of a Jira issue.
type Issue struct {
	Key     string `json:"key"`
	Summary string `json:"-"`
	Desc    string `json:"-"`
}

type searchResponse struct {
	Issues []struct {
		Key    string `json:"key"`
		Fields struct {
			Summary     string `json:"summary"`
			Description any    `json:"description"`
		} `json:"fields"`
	} `json:"issues"`
}

// FindAssigned returns issues in the given project assigned to the
// given user with the given status. Uses JQL search.
func FindAssigned(cfg *Config, project, assignee, status string) ([]Issue, error) {
	jql := fmt.Sprintf("project = %s AND assignee = '%s' AND status = '%s'",
		project, assignee, status)
	query := url.Values{"jql": {jql}, "fields": {"summary,description"}, "maxResults": {"20"}}
	reqURL := fmt.Sprintf("%s/rest/api/3/search?%s",
		strings.TrimRight(cfg.BaseURL, "/"), query.Encode())

	req, _ := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("Authorization", cfg.authHeader())
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jira: search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira: search status %d: %s", resp.StatusCode, string(body))
	}

	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("jira: decode search: %w", err)
	}

	issues := make([]Issue, 0, len(sr.Issues))
	for _, i := range sr.Issues {
		desc := ""
		if s, ok := i.Fields.Description.(string); ok {
			desc = s
		}
		issues = append(issues, Issue{
			Key:     i.Key,
			Summary: i.Fields.Summary,
			Desc:    desc,
		})
	}
	return issues, nil
}

// Transition moves an issue to a new status by name (e.g., "In Progress",
// "Done"). It first fetches available transitions and picks the one
// matching the target name (case-insensitive).
func Transition(cfg *Config, issueKey, targetStatus string) error {
	// Get available transitions.
	tURL := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions",
		strings.TrimRight(cfg.BaseURL, "/"), issueKey)
	req, _ := http.NewRequest("GET", tURL, nil)
	req.Header.Set("Authorization", cfg.authHeader())
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("jira: get transitions: %w", err)
	}
	defer resp.Body.Close()

	var tr struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"transitions"`
	}
	json.NewDecoder(resp.Body).Decode(&tr)

	// Find the matching transition.
	var transitionID string
	for _, t := range tr.Transitions {
		if strings.EqualFold(t.Name, targetStatus) {
			transitionID = t.ID
			break
		}
	}
	if transitionID == "" {
		available := make([]string, 0, len(tr.Transitions))
		for _, t := range tr.Transitions {
			available = append(available, t.Name)
		}
		return fmt.Errorf("jira: no transition to %q for %s (available: %v)",
			targetStatus, issueKey, available)
	}

	// Execute the transition.
	payload := fmt.Sprintf(`{"transition":{"id":"%s"}}`, transitionID)
	req2, _ := http.NewRequest("POST", tURL, strings.NewReader(payload))
	req2.Header.Set("Authorization", cfg.authHeader())
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := client.Do(req2)
	if err != nil {
		return fmt.Errorf("jira: transition: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		body, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("jira: transition %s to %q: status %d: %s",
			issueKey, targetStatus, resp2.StatusCode, string(body))
	}
	return nil
}
