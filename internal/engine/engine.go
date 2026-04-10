// Package engine defines the interface for agent runtime wrappers.
//
// procuracy does not implement agent runtimes — it wraps existing CLI
// tools (Claude Code, Codex, OpenClaw, OpenCode) with trust guardrails
// derived from the contractor's manifest. Each engine implementation
// knows how to spawn its CLI, configure its permissions from a
// capability.Set, stream its output into audit log entries, enforce
// cost limits, and kill the subprocess on demand.
package engine

import (
	"context"

	"github.com/procuracy/procuracy/internal/audit"
	"github.com/procuracy/procuracy/internal/capability"
)

// Config holds everything an engine needs to run a task.
type Config struct {
	// Manifest-derived fields
	Model      string  // e.g. "claude-opus-4-6"
	Workspace  string  // absolute path to the contractor's workspace
	MaxCostUSD float64 // cost_limit_per_task_usd from the manifest

	// Capability set — the engine uses this to derive its tool/permission config
	Caps *capability.Set

	// The prompt to execute (loaded from the handler's prompt file)
	Prompt string

	// The contractor name (for audit entries)
	Contractor string

	// Audit writer — the engine writes entries here for every action
	AuditWriter *audit.Writer
}

// Result is returned when the engine finishes (success or failure).
type Result struct {
	Success    bool
	ExitCode   int
	TotalCost  float64 // USD
	Turns      int
	DurationMS int64
	Error      string
}

// Engine is the interface that each agent CLI wrapper implements.
// The Run method blocks until the agent finishes or the context is
// cancelled (which triggers SIGTERM to the child process — the kill
// switch).
type Engine interface {
	// Name returns the engine identifier (e.g. "claude-code", "codex").
	Name() string

	// Available reports whether the engine's CLI is on PATH.
	Available() bool

	// Run executes a task. It blocks until the agent finishes or ctx
	// is cancelled. All actions are streamed to the audit writer as
	// they happen. The context cancellation is the kill switch:
	// cancelling ctx sends SIGTERM to the child process.
	Run(ctx context.Context, cfg Config) (*Result, error)
}
