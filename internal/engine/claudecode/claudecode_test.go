package claudecode

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/procuracy/procuracy/internal/audit"
	"github.com/procuracy/procuracy/internal/capability"
	"github.com/procuracy/procuracy/internal/engine"
)

func TestBuildArgs(t *testing.T) {
	cfg := engine.Config{
		Model:      "claude-sonnet-4-6",
		MaxCostUSD: 5.0,
		Prompt:     "review the stale PRs",
	}
	args := buildArgs(cfg)

	// Must contain non-interactive flags
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-p") {
		t.Error("missing -p flag")
	}
	if !strings.Contains(joined, "stream-json") {
		t.Error("missing stream-json output format")
	}
	if !strings.Contains(joined, "--verbose") {
		t.Error("missing --verbose")
	}
	if !strings.Contains(joined, "--model claude-sonnet-4-6") {
		t.Errorf("missing model flag, got: %s", joined)
	}
	if !strings.Contains(joined, "--max-budget-usd 5.00") {
		t.Errorf("missing budget flag, got: %s", joined)
	}
	// Prompt must be the last arg
	if args[len(args)-1] != "review the stale PRs" {
		t.Errorf("prompt should be last arg, got: %q", args[len(args)-1])
	}
	// Agent sub-spawning should be disallowed
	if !strings.Contains(joined, "--disallowedTools Agent") {
		t.Errorf("Agent tool should be disallowed, got: %s", joined)
	}
}

func TestBuildArgsNoModel(t *testing.T) {
	cfg := engine.Config{Prompt: "do stuff"}
	args := buildArgs(cfg)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--model") {
		t.Error("should not include --model when empty")
	}
}

func TestBuildSystemPromptWithDenials(t *testing.T) {
	caps, err := capability.Resolve(capability.Scopes{
		"github": {"read:org/*", "pr:org/*", "merge:none", "write:none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt(caps)
	if !strings.Contains(prompt, "MUST NOT") {
		t.Error("system prompt should contain MUST NOT for denials")
	}
	if !strings.Contains(prompt, "merge") {
		t.Error("system prompt should mention denied 'merge' verb")
	}
	if !strings.Contains(prompt, "write") {
		t.Error("system prompt should mention denied 'write' verb")
	}
	if !strings.Contains(prompt, "read, pr") || !strings.Contains(prompt, "pr, read") {
		// granted verbs should be listed (order may vary since they're sorted)
		if !strings.Contains(prompt, "read") || !strings.Contains(prompt, "pr") {
			t.Errorf("system prompt should list granted verbs, got: %s", prompt)
		}
	}
	if !strings.Contains(prompt, "audit log") {
		t.Error("system prompt should mention the audit log")
	}
}

func TestBuildSystemPromptNoCaps(t *testing.T) {
	prompt := buildSystemPrompt(nil)
	if prompt != "" {
		t.Errorf("nil caps should produce empty prompt, got: %q", prompt)
	}
}

func TestProcessEventToolUse(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	w, err := audit.Open(logPath, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	event := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","id":"tu_123","input":{"file_path":"/src/main.go"}}]}}`
	var result engine.Result
	processEvent([]byte(event), w, &result)

	// Should have written 1 audit entry (plus the anchor = 2 total)
	if w.Sequence() != 2 {
		t.Errorf("expected 2 entries (anchor + tool_call), got %d", w.Sequence())
	}
}

func TestProcessEventResult(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	w, err := audit.Open(logPath, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	event := `{"type":"result","subtype":"success","total_cost_usd":0.0345,"num_turns":5,"duration_ms":12345,"stop_reason":"end_turn"}`
	var result engine.Result
	processEvent([]byte(event), w, &result)

	if result.TotalCost != 0.0345 {
		t.Errorf("TotalCost = %f, want 0.0345", result.TotalCost)
	}
	if result.Turns != 5 {
		t.Errorf("Turns = %d, want 5", result.Turns)
	}
}

func TestProcessEventBadJSON(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")
	w, err := audit.Open(logPath, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	processEvent([]byte("{bad json"), w, &engine.Result{})

	// Should have written an error entry
	if w.Sequence() != 2 {
		t.Errorf("expected 2 entries (anchor + error), got %d", w.Sequence())
	}
}

func TestEngineAvailable(t *testing.T) {
	e := New()
	if e.Name() != "claude-code" {
		t.Errorf("Name() = %q", e.Name())
	}
	// Available() depends on whether claude is on PATH — just make sure it doesn't panic
	_ = e.Available()
}
