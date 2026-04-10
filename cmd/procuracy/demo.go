package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/procuracy/procuracy/internal/audit"
)

const sampleManifest = `name: aria
display_name: "Aria — Docs Maintainer"
description: |
  Keeps API docs in sync with code. Reviews stale PR descriptions.
  Drafts changelog entries on release.

identity:
  github_username: aria-acme

scopes:
  github:
    - read:org/*
    - write:org/docs/**
    - pr:org/docs
    - merge:none              # explicit denial — adapter cannot merge

triggers:
  - on: github.pull_request.merged
    where: files matches 'src/api/**'
    do: review_doc_drift

runtime:
  engine: claude-code
  model: claude-opus-4-6
  workspace: /tmp/procuracy/aria
  cost_limit_daily_usd: 50
  cost_limit_per_task_usd: 5

handlers:
  review_doc_drift:
    type: claude_code
    prompt: prompts/review.md
`

func cmdDemo(stdout, stderr io.Writer) int {
	base := filepath.Join(os.TempDir(), "procuracy-demo")
	ariaDir := filepath.Join(base, "aria")

	// Clean any previous run.
	os.RemoveAll(base)
	if err := os.MkdirAll(ariaDir, 0755); err != nil {
		fmt.Fprintf(stderr, "demo: create dir: %v\n", err)
		return 1
	}

	// Write the sample manifest.
	manifestPath := filepath.Join(ariaDir, "procuracy.yaml")
	if err := os.WriteFile(manifestPath, []byte(sampleManifest), 0644); err != nil {
		fmt.Fprintf(stderr, "demo: write manifest: %v\n", err)
		return 1
	}

	// Write a realistic audit log with several entry types.
	logPath := filepath.Join(ariaDir, "audit.jsonl")
	w, err := audit.Open(logPath, "aria")
	if err != nil {
		fmt.Fprintf(stderr, "demo: open audit log: %v\n", err)
		return 1
	}
	entries := []audit.Entry{
		{
			Type:        audit.TypeToolCall,
			Integration: "github",
			Verb:        "read",
			Resource:    "org/acme/docs/PR-42",
			Result:      audit.ResultOK,
			Details:     map[string]any{"pr_number": 42, "author": "alice", "title": "Update API docs for v2"},
		},
		{
			Type:        audit.TypeToolCall,
			Integration: "github",
			Verb:        "write",
			Resource:    "org/acme/docs/api/endpoints.md",
			Result:      audit.ResultOK,
			Details:     map[string]any{"action": "update_file", "lines_added": 12, "lines_removed": 3},
		},
		{
			Type:    audit.TypeCost,
			CostUSD: 0.0234,
			Result:  audit.ResultOK,
			Details: map[string]any{"model": "claude-opus-4-6", "input_tokens": 1500, "output_tokens": 300},
		},
		{
			Type:        audit.TypeToolCall,
			Integration: "github",
			Verb:        "pr",
			Resource:    "org/acme/docs/PR-99",
			Result:      audit.ResultOK,
			Details:     map[string]any{"action": "create_pr", "title": "Sync API docs with v2 endpoints", "base": "main"},
		},
		{
			Type:    audit.TypeCostBlocked,
			CostUSD: 2.50,
			Result:  audit.ResultBlocked,
			Error:   "per-task cost limit reached ($5.00)",
			Details: map[string]any{"model": "claude-opus-4-6", "requested_usd": 2.50, "spent_usd": 4.85, "limit_usd": 5.00},
		},
	}
	for _, e := range entries {
		if err := w.Append(e); err != nil {
			fmt.Fprintf(stderr, "demo: append entry: %v\n", err)
			return 1
		}
	}
	w.Close()

	fmt.Fprintf(stdout, `procuracy demo — try the trust infrastructure in 30 seconds

Created:
  %s
  %s

1. Validate the manifest (schema + scope verbs checked):

   procuracy validate %s

2. Verify the audit log (hash-chain integrity check):

   procuracy verify %s

3. See tamper detection in action — corrupt one byte and re-verify:

   sed -i.bak 's/alice/malice/' %s
   procuracy verify %s

   The chain breaks at the tampered entry. This is what makes the
   audit log a trust layer, not just a log file.

4. Explore the files:

   cat %s    # the contractor manifest
   cat %s    # the hash-chained audit log (one JSON object per line)

`, manifestPath, logPath,
		manifestPath,
		logPath,
		logPath, logPath,
		manifestPath, logPath,
	)
	return 0
}
