package manifest

import (
	"strings"
	"testing"
)

const validManifest = `
name: aria
display_name: "Aria — Docs Maintainer"
identity:
  github_username: aria-acme
  slack_handle: aria
scopes:
  github:
    - read:org/*
    - write:org/docs/**
  slack:
    - post:#engineering
triggers:
  - on: github.pull_request.merged
    where: files matches 'src/api/**'
    do: review_doc_drift
runtime:
  engine: claude-code
  model: claude-opus-4-6
  workspace: /var/procuracy/aria
  cost_limit_daily_usd: 50
  cost_limit_per_task_usd: 5
handlers:
  review_doc_drift:
    type: claude_code
    prompt: prompts/review_doc_drift.md
`

func TestParseValid(t *testing.T) {
	m, err := Parse([]byte(validManifest), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "aria" {
		t.Errorf("name = %q, want aria", m.Name)
	}
	if len(m.Triggers) != 1 {
		t.Errorf("triggers len = %d, want 1", len(m.Triggers))
	}
	if m.Runtime.Engine != "claude-code" {
		t.Errorf("engine = %q", m.Runtime.Engine)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(string) string
		wantSub string
	}{
		{
			name:    "missing name",
			mutate:  func(s string) string { return strings.Replace(s, "name: aria", "", 1) },
			wantSub: "name is required",
		},
		{
			name:    "bad name format",
			mutate:  func(s string) string { return strings.Replace(s, "name: aria", "name: Aria!", 1) },
			wantSub: "must match",
		},
		{
			name:    "relative workspace",
			mutate:  func(s string) string { return strings.Replace(s, "/var/procuracy/aria", "./aria", 1) },
			wantSub: "absolute path",
		},
		{
			name: "per-task cost exceeds daily",
			mutate: func(s string) string {
				return strings.Replace(s, "cost_limit_per_task_usd: 5", "cost_limit_per_task_usd: 100", 1)
			},
			wantSub: "must be <= cost_limit_daily_usd",
		},
		{
			name: "trigger references undefined handler",
			mutate: func(s string) string {
				return strings.Replace(s, "do: review_doc_drift", "do: ghost_handler", 1)
			},
			wantSub: "undefined handler",
		},
		{
			name: "scope without identity",
			mutate: func(s string) string {
				return strings.Replace(s, "  github_username: aria-acme\n", "", 1)
			},
			wantSub: "scopes.github requires identity.github_username",
		},
		{
			name: "unknown integration",
			mutate: func(s string) string {
				return strings.Replace(s, "  slack:\n    - post:#engineering\n", "  myrandomtool:\n    - read:foo\n", 1)
			},
			wantSub: "unknown integration",
		},
		{
			name: "unknown top-level key",
			mutate: func(s string) string {
				return s + "\nfoo_bar: 1\n"
			},
			wantSub: "field foo_bar not found",
		},
		{
			name: "schedule trigger without cron",
			mutate: func(s string) string {
				return strings.Replace(s,
					"  - on: github.pull_request.merged\n    where: files matches 'src/api/**'\n    do: review_doc_drift",
					"  - on: schedule\n    do: review_doc_drift", 1)
			},
			wantSub: "no cron",
		},
		{
			name: "unreferenced handler",
			mutate: func(s string) string {
				return s + "  orphan:\n    type: claude_code\n    prompt: x.md\n"
			},
			wantSub: "never referenced",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.mutate(validManifest)), "")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}
