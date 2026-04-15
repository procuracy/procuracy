package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/procuracy/procuracy/internal/manifest"
)

const defaultPromptContent = `# Default handler prompt

You are an AI contractor managed by procuracy. Your job is described
in the procuracy.yaml manifest that governs this workspace.

Follow these rules:
- Only modify files within your scoped resource paths.
- If you are unsure whether an action is in scope, stop and report a blocker.
- Write clear, concise commit messages.
- Never commit secrets, credentials, or .env files.
`

func cmdInit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	scanner := bufio.NewScanner(stdin)
	prompt := func(label, fallback string) string {
		if fallback != "" {
			fmt.Fprintf(stdout, "%s [%s]: ", label, fallback)
		} else {
			fmt.Fprintf(stdout, "%s: ", label)
		}
		if !scanner.Scan() {
			return fallback
		}
		v := strings.TrimSpace(scanner.Text())
		if v == "" {
			return fallback
		}
		return v
	}

	fmt.Fprintln(stdout, "procuracy init — scaffold a new contractor")
	fmt.Fprintln(stdout)

	name := prompt("Contractor name (lowercase, a-z0-9 and hyphens)", "")
	if name == "" {
		fmt.Fprintln(stderr, "init: contractor name is required")
		return 1
	}
	repo := prompt("GitHub repo (org/repo)", "")
	if repo == "" {
		fmt.Fprintln(stderr, "init: GitHub repo is required")
		return 1
	}
	capsStr := prompt("Allowed operations (comma-separated: read, write, pr, merge)", "read, write, pr")
	dailyLimit := prompt("Daily cost limit in USD", "50")
	taskLimit := prompt("Per-task cost limit in USD", "5")
	triggerEvent := prompt("Trigger event", "github.pull_request.merged")
	promptPath := prompt("Prompt file path", "prompts/default.md")

	// Build scopes from the comma-separated capability list.
	caps := strings.Split(capsStr, ",")
	var scopeLines []string
	hasMerge := false
	for _, c := range caps {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if c == "merge" {
			hasMerge = true
		}
		scopeLines = append(scopeLines, fmt.Sprintf("    - %s:%s/**", c, repo))
	}
	if !hasMerge {
		scopeLines = append(scopeLines, "    - merge:none")
	}

	// Derive the GitHub username from the contractor name + org.
	parts := strings.SplitN(repo, "/", 2)
	org := parts[0]
	ghUser := name + "-" + org

	// Build the handler name from the trigger event.
	handlerName := "handle_task"
	triggerDo := handlerName

	yaml := fmt.Sprintf(`name: %s

identity:
  github_username: %s

scopes:
  github:
%s

triggers:
  - on: %s
    do: %s

runtime:
  engine: claude-code
  workspace: /var/procuracy/%s
  cost_limit_daily_usd: %s
  cost_limit_per_task_usd: %s

handlers:
  %s:
    type: claude_code
    prompt: %s
`, name, ghUser, strings.Join(scopeLines, "\n"), triggerEvent, triggerDo, name, dailyLimit, taskLimit, handlerName, promptPath)

	// Create the directory structure.
	outDir := filepath.Join(".", name)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		fmt.Fprintf(stderr, "init: create directory: %v\n", err)
		return 1
	}
	manifestPath := filepath.Join(outDir, "procuracy.yaml")
	if err := os.WriteFile(manifestPath, []byte(yaml), 0644); err != nil {
		fmt.Fprintf(stderr, "init: write manifest: %v\n", err)
		return 1
	}

	// Create the prompt directory and starter prompt file.
	promptDir := filepath.Join(outDir, filepath.Dir(promptPath))
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		fmt.Fprintf(stderr, "init: create prompt dir: %v\n", err)
		return 1
	}
	promptFullPath := filepath.Join(outDir, promptPath)
	if err := os.WriteFile(promptFullPath, []byte(defaultPromptContent), 0644); err != nil {
		fmt.Fprintf(stderr, "init: write prompt: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "\nCreated:\n  %s\n  %s\n", manifestPath, promptFullPath)

	// Validate the generated manifest as a sanity check.
	if _, err := manifest.Load(manifestPath); err != nil {
		fmt.Fprintf(stderr, "\nwarning: generated manifest has validation errors: %v\n", err)
		fmt.Fprintf(stderr, "Edit %s to fix, then run: procuracy validate %s\n", manifestPath, manifestPath)
		return 0
	}
	fmt.Fprintf(stdout, "\nValidation passed. Next steps:\n")
	fmt.Fprintf(stdout, "  procuracy validate %s   # re-validate after edits\n", manifestPath)
	fmt.Fprintf(stdout, "  procuracy run %s               # run with guardrails\n", outDir)
	return 0
}
