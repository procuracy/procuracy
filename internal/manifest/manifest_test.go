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

func TestIdentityModeDefaultsToDirect(t *testing.T) {
	m, err := Parse([]byte(validManifest), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Identity.Mode != IdentityModeDirect {
		t.Errorf("Mode = %q, want %q", m.Identity.Mode, IdentityModeDirect)
	}
	if w := m.Warnings(); len(w) != 0 {
		t.Errorf("default manifest produced warnings: %v", w)
	}
}

func TestIdentityModeIdPManagedParsesAndWarns(t *testing.T) {
	src := strings.Replace(validManifest,
		"identity:\n  github_username: aria-acme",
		"identity:\n  mode: idp-managed\n  github_username: aria-acme",
		1)
	m, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("idp-managed should parse cleanly, got: %v", err)
	}
	if m.Identity.Mode != IdentityModeIdPManaged {
		t.Errorf("Mode = %q, want %q", m.Identity.Mode, IdentityModeIdPManaged)
	}
	ws := m.Warnings()
	if len(ws) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(ws), ws)
	}
	if !strings.Contains(ws[0], "idp-managed") || !strings.Contains(ws[0], "enterprise-provisioning.md") {
		t.Errorf("warning text does not point at the design doc: %q", ws[0])
	}
}

func TestIdentityModeUnknownIsRejected(t *testing.T) {
	src := strings.Replace(validManifest,
		"identity:\n  github_username: aria-acme",
		"identity:\n  mode: hybrid\n  github_username: aria-acme",
		1)
	_, err := Parse([]byte(src), "")
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if !strings.Contains(err.Error(), "not recognized") {
		t.Errorf("error = %v, want 'not recognized'", err)
	}
}

func TestStateBlockRoundTrips(t *testing.T) {
	src := validManifest + `
state:
  phase: requested
  requested_by: alice@company.com
  approval_ticket: COMPANY-1234
  history:
    - 2026-04-09T10:00Z drafted by alice@company.com
    - 2026-04-09T10:05Z requested via procuracy request
`
	m, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.State == nil {
		t.Fatal("State is nil")
	}
	if m.State.Phase != StatePhaseRequested {
		t.Errorf("Phase = %q, want %q", m.State.Phase, StatePhaseRequested)
	}
	if m.State.RequestedBy != "alice@company.com" {
		t.Errorf("RequestedBy = %q", m.State.RequestedBy)
	}
	if m.State.ApprovalTicket != "COMPANY-1234" {
		t.Errorf("ApprovalTicket = %q", m.State.ApprovalTicket)
	}
	if len(m.State.History) != 2 {
		t.Errorf("History len = %d, want 2", len(m.State.History))
	}
	ws := m.Warnings()
	if len(ws) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(ws), ws)
	}
	if !strings.Contains(ws[0], "state block") {
		t.Errorf("warning does not mention state block: %q", ws[0])
	}
}

func TestStatePhaseUnknownIsRejected(t *testing.T) {
	src := validManifest + `
state:
  phase: yolo
`
	_, err := Parse([]byte(src), "")
	if err == nil {
		t.Fatal("expected error for unknown state.phase")
	}
	if !strings.Contains(err.Error(), "not recognized") {
		t.Errorf("error = %v", err)
	}
}

func TestEmptyStateBlockProducesNoWarning(t *testing.T) {
	// A state: {} is allowed and silent — only populated state blocks warn.
	src := validManifest + "\nstate: {}\n"
	m, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if w := m.Warnings(); len(w) != 0 {
		t.Errorf("empty state block produced warnings: %v", w)
	}
}

func TestBothWarningsAtOnce(t *testing.T) {
	src := strings.Replace(validManifest,
		"identity:\n  github_username: aria-acme",
		"identity:\n  mode: idp-managed\n  github_username: aria-acme",
		1) + `
state:
  phase: draft
  requested_by: alice@company.com
`
	m, err := Parse([]byte(src), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ws := m.Warnings()
	if len(ws) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(ws), ws)
	}
}
