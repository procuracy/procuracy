package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procuracy/procuracy/internal/capability"
)

const validGroupsYAML = `
bot-docs-readonly:
  description: "Can read all repos, write only docs/, never merge"
  scopes:
    github:
      - read:org/*
      - write:org/*/docs/**
      - merge:none
  cost_limit_daily_usd: 25
  cost_limit_per_task_usd: 3
  notifications:
    slack_webhook: https://hooks.slack.com/test

bot-triage:
  description: "Read-only, can comment on issues"
  scopes:
    github:
      - read:org/*
      - pr:org/*
      - merge:none
  cost_limit_daily_usd: 15
  cost_limit_per_task_usd: 1
`

func TestParseGroups(t *testing.T) {
	g, err := ParseGroups([]byte(validGroupsYAML))
	if err != nil {
		t.Fatalf("ParseGroups: %v", err)
	}
	if len(g) != 2 {
		t.Fatalf("got %d groups, want 2", len(g))
	}
	docs, ok := g["bot-docs-readonly"]
	if !ok {
		t.Fatal("missing bot-docs-readonly")
	}
	if len(docs.Scopes["github"]) != 3 {
		t.Errorf("bot-docs-readonly github scopes = %d, want 3", len(docs.Scopes["github"]))
	}
	if *docs.CostLimitDailyUSD != 25 {
		t.Errorf("daily limit = %f, want 25", *docs.CostLimitDailyUSD)
	}
	if docs.Notifications == nil || docs.Notifications.SlackWebhook == "" {
		t.Error("missing notifications.slack_webhook")
	}
}

func TestGroupCostValidation(t *testing.T) {
	_, err := ParseGroups([]byte(`
bad-group:
  scopes:
    github: [read:org/*]
  cost_limit_daily_usd: 5
  cost_limit_per_task_usd: 10
`))
	if err == nil {
		t.Fatal("expected error for per-task > daily")
	}
	if !strings.Contains(err.Error(), "must be <=") {
		t.Errorf("error = %v", err)
	}
}

func TestGroupNegativeCost(t *testing.T) {
	_, err := ParseGroups([]byte(`
bad:
  cost_limit_daily_usd: -5
`))
	if err == nil {
		t.Fatal("expected error for negative cost")
	}
}

func TestLoadGroupsFileNotExist(t *testing.T) {
	g, err := LoadGroups("/nonexistent/groups.yaml")
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if len(g) != 0 {
		t.Errorf("expected empty groups, got %d", len(g))
	}
}

func TestApplyGroupInheritsScopes(t *testing.T) {
	groups, _ := ParseGroups([]byte(validGroupsYAML))
	m := &Manifest{
		Name:  "aria",
		Group: "bot-docs-readonly",
		Identity: Identity{
			GitHubUsername: "aria-bot",
		},
		Triggers: []Trigger{{On: "schedule", Cron: "0 9 * * 1-5", Do: "work"}},
		Runtime: Runtime{
			Engine:    "claude-code",
			Workspace: "/tmp/aria",
		},
		Handlers: map[string]Handler{
			"work": {Type: "claude_code", Prompt: "p.md"},
		},
	}
	if err := ApplyGroup(m, groups); err != nil {
		t.Fatalf("ApplyGroup: %v", err)
	}
	if len(m.Scopes) == 0 {
		t.Fatal("scopes should be inherited from group")
	}
	if len(m.Scopes["github"]) != 3 {
		t.Errorf("github scopes = %d, want 3", len(m.Scopes["github"]))
	}
	if m.Runtime.CostLimitDailyUSD != 25 {
		t.Errorf("daily cost = %f, want 25", m.Runtime.CostLimitDailyUSD)
	}
	if m.Runtime.CostLimitPerTaskUSD != 3 {
		t.Errorf("per-task cost = %f, want 3", m.Runtime.CostLimitPerTaskUSD)
	}
	if m.Notifications == nil {
		t.Error("notifications should be inherited from group")
	}
}

func TestApplyGroupManifestOverrides(t *testing.T) {
	groups, _ := ParseGroups([]byte(validGroupsYAML))
	m := &Manifest{
		Name:  "aria",
		Group: "bot-docs-readonly",
		Scopes: capability.Scopes{
			"github": {"read:org/*"},
		},
		Runtime: Runtime{
			CostLimitDailyUSD:   100,
			CostLimitPerTaskUSD: 10,
		},
		Notifications: &Notifications{
			SlackWebhook: "https://my-own-webhook",
		},
	}
	if err := ApplyGroup(m, groups); err != nil {
		t.Fatal(err)
	}
	// Manifest values should win — not overwritten by group.
	if len(m.Scopes["github"]) != 1 {
		t.Errorf("manifest scopes should not be overwritten, got %d", len(m.Scopes["github"]))
	}
	if m.Runtime.CostLimitDailyUSD != 100 {
		t.Errorf("manifest daily cost should not be overwritten, got %f", m.Runtime.CostLimitDailyUSD)
	}
	if m.Notifications.SlackWebhook != "https://my-own-webhook" {
		t.Errorf("manifest notifications should not be overwritten, got %q", m.Notifications.SlackWebhook)
	}
}

func TestApplyGroupUndefined(t *testing.T) {
	groups, _ := ParseGroups([]byte(validGroupsYAML))
	m := &Manifest{Name: "aria", Group: "nonexistent-group"}
	err := ApplyGroup(m, groups)
	if err == nil {
		t.Fatal("expected error for undefined group")
	}
	if !strings.Contains(err.Error(), "nonexistent-group") {
		t.Errorf("error should mention group name, got: %v", err)
	}
}

func TestApplyGroupNoGroupField(t *testing.T) {
	groups, _ := ParseGroups([]byte(validGroupsYAML))
	m := &Manifest{Name: "aria"}
	if err := ApplyGroup(m, groups); err != nil {
		t.Fatalf("no group field should be a no-op, got: %v", err)
	}
}

func TestLoadEndToEndWithGroups(t *testing.T) {
	dir := t.TempDir()

	// Write groups.yaml
	groupsContent := `
bot-docs:
  scopes:
    github:
      - read:org/*
      - write:org/*/docs/**
      - merge:none
  cost_limit_daily_usd: 25
  cost_limit_per_task_usd: 3
  notifications:
    slack_webhook: https://hooks.slack.com/test
`
	os.WriteFile(filepath.Join(dir, "groups.yaml"), []byte(groupsContent), 0644)

	// Write a manifest that references the group
	manifestContent := `
name: aria
group: bot-docs
identity:
  github_username: aria-bot
triggers:
  - on: schedule
    cron: "0 9 * * 1-5"
    do: sync_docs
runtime:
  engine: claude-code
  workspace: /tmp/aria
handlers:
  sync_docs:
    type: claude_code
    prompt: prompts/sync.md
`
	manifestPath := filepath.Join(dir, "procuracy.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	m, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Scopes["github"]) != 3 {
		t.Errorf("scopes should be inherited: got %d", len(m.Scopes["github"]))
	}
	if m.Runtime.CostLimitDailyUSD != 25 {
		t.Errorf("daily cost should be inherited: got %f", m.Runtime.CostLimitDailyUSD)
	}
	if m.Notifications == nil || m.Notifications.SlackWebhook == "" {
		t.Error("notifications should be inherited")
	}
}
