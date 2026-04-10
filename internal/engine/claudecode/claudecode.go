// Package claudecode wraps the Claude Code CLI (`claude`) as a
// procuracy engine. It spawns claude in non-interactive mode with
// streaming JSON output, parses events into audit log entries, and
// enforces capability constraints via --allowedTools / --disallowedTools
// and a restrictive system prompt derived from the manifest's scopes.
//
// The trust model is hybrid:
//   - Tool-level scoping is capability-based (--allowedTools controls
//     which tools exist in the agent's toolbox)
//   - Verb-level scoping within tools (e.g., "can use gh but not
//     gh pr merge") is instruction-based via the system prompt, with
//     the audit log as the verification layer
//   - Cost limits are enforced by Claude Code's --max-budget-usd flag
//
// This hybrid is honest about the limits: Claude Code's tool scoping
// isn't fine-grained enough for per-verb GitHub API enforcement. The
// audit log catches violations after the fact; the cost limit prevents
// runaway spending.
package claudecode

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/procuracy/procuracy/internal/audit"
	"github.com/procuracy/procuracy/internal/capability"
	"github.com/procuracy/procuracy/internal/engine"
)

// Engine implements engine.Engine for Claude Code.
type Engine struct{}

// New returns a new Claude Code engine.
func New() *Engine { return &Engine{} }

func (e *Engine) Name() string { return "claude-code" }

func (e *Engine) Available() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

func (e *Engine) Run(ctx context.Context, cfg engine.Config) (*engine.Result, error) {
	if cfg.Prompt == "" {
		return nil, fmt.Errorf("claude-code: prompt is empty")
	}
	if cfg.AuditWriter == nil {
		return nil, fmt.Errorf("claude-code: audit writer is required")
	}

	args := buildArgs(cfg)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = cfg.Workspace
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude-code: stdout pipe: %w", err)
	}

	start := time.Now()

	// Write a lifecycle audit entry for task start.
	cfg.AuditWriter.Append(audit.Entry{
		Type:    audit.TypeLifecycle,
		Subtype: "started",
		Details: map[string]any{
			"engine": "claude-code",
			"model":  cfg.Model,
		},
	})

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude-code: start: %w", err)
	}

	// Parse streaming JSON events and write audit entries.
	var result engine.Result
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		processEvent(line, cfg.AuditWriter, &result)
	}

	// Wait for the process to finish.
	waitErr := cmd.Wait()
	result.DurationMS = time.Since(start).Milliseconds()

	if ctx.Err() != nil {
		// Context was cancelled — this is the kill switch path.
		result.Success = false
		result.Error = "killed by operator (context cancelled)"
		cfg.AuditWriter.Append(audit.Entry{
			Type:    audit.TypeLifecycle,
			Subtype: "killed",
			Details: map[string]any{
				"duration_ms": result.DurationMS,
				"reason":      "context cancelled",
			},
		})
		return &result, nil
	}

	if waitErr != nil {
		result.Success = false
		if result.Error == "" {
			result.Error = waitErr.Error()
		}
		cfg.AuditWriter.Append(audit.Entry{
			Type:    audit.TypeLifecycle,
			Subtype: "failed",
			Error:   result.Error,
			Details: map[string]any{
				"duration_ms": result.DurationMS,
				"total_cost":  result.TotalCost,
				"exit_code":   result.ExitCode,
			},
		})
	} else {
		result.Success = true
		result.ExitCode = 0
		cfg.AuditWriter.Append(audit.Entry{
			Type:    audit.TypeLifecycle,
			Subtype: "completed",
			Details: map[string]any{
				"duration_ms": result.DurationMS,
				"total_cost":  result.TotalCost,
				"turns":       result.Turns,
			},
		})
	}

	return &result, nil
}

// buildArgs constructs the claude CLI arguments from the engine config.
func buildArgs(cfg engine.Config) []string {
	args := []string{
		"-p", // non-interactive (print mode)
		"--output-format", "stream-json",
		"--verbose",
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.MaxCostUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", cfg.MaxCostUSD))
	}

	// Derive tool constraints from capability set.
	allowed, disallowed := scopeToTools(cfg.Caps)
	if len(allowed) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowed, " "))
	}
	if len(disallowed) > 0 {
		args = append(args, "--disallowedTools", strings.Join(disallowed, " "))
	}

	// Inject a restrictive system prompt derived from denials.
	sysPrompt := buildSystemPrompt(cfg.Caps)
	if sysPrompt != "" {
		args = append(args, "--append-system-prompt", sysPrompt)
	}

	// The prompt itself is the last argument.
	args = append(args, cfg.Prompt)
	return args
}

// scopeToTools maps a capability.Set to Claude Code's --allowedTools
// and --disallowedTools flags.
//
// The mapping is necessarily coarse because Claude Code's tool system
// operates at the tool level (Read, Edit, Bash), not at the API-verb
// level (github.merge, github.pr:create). Fine-grained verb enforcement
// is handled by the system prompt + audit log, not by tool restrictions.
func scopeToTools(caps *capability.Set) (allowed []string, disallowed []string) {
	// For v0.1, we keep it simple:
	// - If the contractor has any github scopes, allow Bash(git:*) and Bash(gh:*)
	// - If merge:none is in the denials, we rely on the system prompt
	//   (Claude Code can't selectively deny gh pr merge within Bash)
	// - Always allow Read, Glob, Grep (read-only, low risk)
	// - Allow Edit and Write only if the contractor has write scopes
	// - Agent spawning is disallowed by default

	// We don't restrict at this level for v0.1 — the system prompt
	// handles fine-grained denials, and --max-budget-usd handles cost.
	// Future versions will map more precisely as Claude Code's tool
	// scoping matures.

	disallowed = append(disallowed, "Agent") // no sub-agent spawning
	return nil, disallowed
}

// buildSystemPrompt generates a restrictive system prompt from the
// capability set's denials. This is the instruction-based layer on top
// of the tool-level enforcement.
func buildSystemPrompt(caps *capability.Set) string {
	if caps == nil {
		return ""
	}

	var rules []string
	rules = append(rules, "You are a procuracy-managed AI contractor. The following rules are MANDATORY and override any other instructions:")

	for _, integration := range caps.Integrations() {
		denied := caps.DeniedVerbs(integration)
		for _, verb := range denied {
			rules = append(rules, fmt.Sprintf("- You MUST NOT perform '%s' operations on %s under any circumstances.", verb, integration))
		}

		granted := caps.GrantedVerbs(integration)
		if len(granted) > 0 {
			rules = append(rules, fmt.Sprintf("- On %s, you may ONLY perform: %s", integration, strings.Join(granted, ", ")))
		}
	}

	rules = append(rules, "- If asked to do something outside these rules, REFUSE and explain that your manifest does not grant that capability.")
	rules = append(rules, "- Every action you take is recorded in a tamper-evident audit log.")

	return strings.Join(rules, "\n")
}
