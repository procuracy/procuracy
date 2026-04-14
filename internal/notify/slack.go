// Package notify posts status updates to Slack and Jira when
// procuracy agents start, complete, fail, or hit cost limits.
//
// Both integrations use simple HTTP POST — no SDKs, no OAuth flows,
// no bot tokens. Slack uses incoming webhooks; Jira uses REST API
// with Basic auth (email + API token). This is intentionally minimal
// so an org can adopt notifications in 5 minutes by pasting a
// webhook URL into their manifest.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Event represents something that happened during a procuracy run.
type Event struct {
	Type       string  // "start", "complete", "fail", "cost_blocked"
	Contractor string  // the agent's name from the manifest
	Engine     string  // e.g. "claude-code"
	Model      string  // e.g. "claude-sonnet-4-6"
	Budget     float64 // cost_limit_per_task_usd
	Cost       float64 // actual cost (for complete/fail)
	Turns      int     // number of turns (for complete)
	DurationMS int64   // duration in ms (for complete/fail)
	Error      string  // error message (for fail/cost_blocked)
	AuditPath  string  // path to the audit log
	AuditCount uint64  // number of audit entries
	JiraKey    string  // e.g. "PROJ-456" (if triggered by Jira)
}

// slackMessage is the Slack incoming webhook payload.
type slackMessage struct {
	Text   string       `json:"text"`
	Blocks []slackBlock `json:"blocks,omitempty"`
}

type slackBlock struct {
	Type string     `json:"type"`
	Text *slackText `json:"text,omitempty"`
}

type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Slack posts a notification to a Slack incoming webhook URL.
// It returns nil if webhookURL is empty (notifications disabled).
func Slack(webhookURL string, ev Event) error {
	if webhookURL == "" {
		return nil
	}
	msg := formatSlackMessage(ev)
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("notify/slack: marshal: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/slack: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("notify/slack: status %d", resp.StatusCode)
	}
	return nil
}

func formatSlackMessage(ev Event) slackMessage {
	var emoji, title, detail string

	switch ev.Type {
	case "start":
		emoji = ":large_green_circle:"
		title = fmt.Sprintf("%s *%s* started working", emoji, ev.Contractor)
		detail = fmt.Sprintf("Engine: `%s` | Model: `%s` | Budget: $%.2f/task",
			ev.Engine, ev.Model, ev.Budget)
		if ev.JiraKey != "" {
			title = fmt.Sprintf("%s *%s* started working on *%s*", emoji, ev.Contractor, ev.JiraKey)
		}

	case "complete":
		emoji = ":white_check_mark:"
		title = fmt.Sprintf("%s *%s* completed", emoji, ev.Contractor)
		detail = fmt.Sprintf("Cost: $%.4f | Turns: %d | Duration: %s",
			ev.Cost, ev.Turns, formatDuration(ev.DurationMS))
		if ev.JiraKey != "" {
			title = fmt.Sprintf("%s *%s* completed *%s*", emoji, ev.Contractor, ev.JiraKey)
		}

	case "fail":
		emoji = ":red_circle:"
		title = fmt.Sprintf("%s *%s* failed", emoji, ev.Contractor)
		detail = fmt.Sprintf("Error: %s\nCost: $%.4f | Duration: %s",
			ev.Error, ev.Cost, formatDuration(ev.DurationMS))
		if ev.JiraKey != "" {
			title = fmt.Sprintf("%s *%s* failed on *%s*", emoji, ev.Contractor, ev.JiraKey)
		}

	case "cost_blocked":
		emoji = ":no_entry:"
		title = fmt.Sprintf("%s *%s* hit cost limit", emoji, ev.Contractor)
		detail = fmt.Sprintf("Budget: $%.2f/task | %s", ev.Budget, ev.Error)
	}

	if ev.AuditCount > 0 {
		detail += fmt.Sprintf("\nAudit: %d entries", ev.AuditCount)
	}

	return slackMessage{
		Text: fmt.Sprintf("%s — %s", title, detail), // fallback
		Blocks: []slackBlock{
			{Type: "section", Text: &slackText{Type: "mrkdwn", Text: title}},
			{Type: "section", Text: &slackText{Type: "mrkdwn", Text: detail}},
		},
	}
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(ms)/60000)
}
