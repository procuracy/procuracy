package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/procuracy/procuracy/internal/audit"
	"github.com/procuracy/procuracy/internal/capability"
	"github.com/procuracy/procuracy/internal/engine"
	"github.com/procuracy/procuracy/internal/engine/claudecode"
	"github.com/procuracy/procuracy/internal/manifest"
	"github.com/procuracy/procuracy/internal/notify"
)

func cmdRun(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: procuracy run <contractor-dir> [--handler <name>] [--jira-ticket KEY]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Runs a contractor's handler using the engine specified in the manifest.")
		fmt.Fprintln(stderr, "The contractor directory must contain a procuracy.yaml manifest.")
		return 2
	}

	dir := args[0]

	// Parse optional flags.
	handlerName := ""
	jiraTicket := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--handler" && i+1 < len(args) {
			handlerName = args[i+1]
			i++
		}
		if args[i] == "--jira-ticket" && i+1 < len(args) {
			jiraTicket = args[i+1]
			i++
		}
	}

	// Load and validate the manifest.
	manifestPath := filepath.Join(dir, "procuracy.yaml")
	m, err := manifest.Load(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "run: %v\n", err)
		return 1
	}

	// Resolve capabilities.
	caps, err := capability.Resolve(m.Scopes)
	if err != nil {
		fmt.Fprintf(stderr, "run: resolve capabilities: %v\n", err)
		return 1
	}

	// Pick the handler.
	handler, prompt, err := resolveHandler(m, dir, handlerName)
	if err != nil {
		fmt.Fprintf(stderr, "run: %v\n", err)
		return 1
	}
	_ = handler // we use the prompt, the handler struct is for future use

	// Pick the engine.
	eng, err := resolveEngine(m.Runtime.Engine)
	if err != nil {
		fmt.Fprintf(stderr, "run: %v\n", err)
		return 1
	}
	if !eng.Available() {
		fmt.Fprintf(stderr, "run: engine %q requires '%s' CLI on PATH but it was not found\n",
			m.Runtime.Engine, "claude")
		return 1
	}

	// Ensure workspace exists.
	if err := os.MkdirAll(m.Runtime.Workspace, 0755); err != nil {
		fmt.Fprintf(stderr, "run: create workspace: %v\n", err)
		return 1
	}

	// Open the audit log.
	auditPath := filepath.Join(dir, "audit.jsonl")
	w, err := audit.Open(auditPath, m.Name)
	if err != nil {
		fmt.Fprintf(stderr, "run: open audit log: %v\n", err)
		return 1
	}
	defer w.Close()

	// Set up context with signal handling for kill switch.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintln(stderr, "\nprocuracy: received signal, killing agent...")
			cancel()
		case <-ctx.Done():
		}
	}()

	// Build the notification helper.
	sendNotify := buildNotifier(m, jiraTicket, stderr)

	// Run.
	fmt.Fprintf(stdout, "procuracy: running %s (engine=%s, model=%s, budget=$%.2f)\n",
		m.Name, m.Runtime.Engine, m.Runtime.Model, m.Runtime.CostLimitPerTaskUSD)

	sendNotify(notify.Event{
		Type:       "start",
		Contractor: m.Name,
		Engine:     m.Runtime.Engine,
		Model:      m.Runtime.Model,
		Budget:     m.Runtime.CostLimitPerTaskUSD,
		JiraKey:    jiraTicket,
	})

	result, err := eng.Run(ctx, engine.Config{
		Model:       m.Runtime.Model,
		Workspace:   m.Runtime.Workspace,
		MaxCostUSD:  m.Runtime.CostLimitPerTaskUSD,
		Caps:        caps,
		Prompt:      prompt,
		Contractor:  m.Name,
		AuditWriter: w,
	})
	if err != nil {
		fmt.Fprintf(stderr, "run: %v\n", err)
		return 1
	}

	// Report result.
	w.Sync()
	ev := notify.Event{
		Contractor: m.Name,
		Engine:     m.Runtime.Engine,
		Model:      m.Runtime.Model,
		Budget:     m.Runtime.CostLimitPerTaskUSD,
		Cost:       result.TotalCost,
		Turns:      result.Turns,
		DurationMS: result.DurationMS,
		AuditPath:  auditPath,
		AuditCount: w.Sequence(),
		JiraKey:    jiraTicket,
	}

	if result.Success {
		ev.Type = "complete"
		sendNotify(ev)
		fmt.Fprintf(stdout, "procuracy: completed (cost=$%.4f, turns=%d, duration=%dms)\n",
			result.TotalCost, result.Turns, result.DurationMS)
		fmt.Fprintf(stdout, "procuracy: audit log at %s (%d entries)\n", auditPath, w.Sequence())
		return 0
	}

	ev.Type = "fail"
	ev.Error = result.Error
	sendNotify(ev)
	fmt.Fprintf(stderr, "procuracy: failed: %s\n", result.Error)
	fmt.Fprintf(stderr, "procuracy: audit log at %s (%d entries)\n", auditPath, w.Sequence())
	return 1
}

// buildNotifier creates a function that sends notifications to Slack
// and/or Jira based on the manifest's notifications config.
func buildNotifier(m *manifest.Manifest, jiraTicket string, stderr io.Writer) func(notify.Event) {
	n := m.Notifications
	return func(ev notify.Event) {
		if n == nil {
			return
		}
		if !n.ShouldNotify(ev.Type) {
			return
		}
		if n.SlackWebhook != "" {
			if err := notify.Slack(n.SlackWebhook, ev); err != nil {
				fmt.Fprintf(stderr, "procuracy: slack notification failed: %v\n", err)
			}
		}
		if n.JiraBaseURL != "" && jiraTicket != "" {
			cfg := &notify.JiraConfig{
				BaseURL: n.JiraBaseURL,
				Email:   n.JiraEmail,
				Token:   n.JiraToken,
			}
			if err := notify.JiraComment(cfg, jiraTicket, ev); err != nil {
				fmt.Fprintf(stderr, "procuracy: jira notification failed: %v\n", err)
			}
		}
	}
}

// resolveHandler picks a handler from the manifest and loads its prompt.
func resolveHandler(m *manifest.Manifest, dir, name string) (manifest.Handler, string, error) {
	if name == "" {
		// Default to the first handler (deterministic: Go maps aren't
		// ordered, so we pick the one referenced by the first trigger).
		if len(m.Triggers) == 0 {
			return manifest.Handler{}, "", fmt.Errorf("manifest has no triggers")
		}
		name = m.Triggers[0].Do
	}
	h, ok := m.Handlers[name]
	if !ok {
		return manifest.Handler{}, "", fmt.Errorf("handler %q not found in manifest", name)
	}
	if h.Prompt == "" {
		return manifest.Handler{}, "", fmt.Errorf("handler %q has no prompt file", name)
	}

	promptPath := filepath.Join(dir, h.Prompt)
	raw, err := os.ReadFile(promptPath)
	if err != nil {
		return manifest.Handler{}, "", fmt.Errorf("read prompt %s: %w", promptPath, err)
	}
	return h, string(raw), nil
}

// resolveEngine returns the engine implementation for the given name.
func resolveEngine(name string) (engine.Engine, error) {
	switch name {
	case "claude-code":
		return claudecode.New(), nil
	default:
		return nil, fmt.Errorf("unknown engine %q (supported: claude-code)", name)
	}
}
