package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/procuracy/procuracy/internal/jira"
	"github.com/procuracy/procuracy/internal/manifest"
	"gopkg.in/yaml.v3"
)

func cmdRequest(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: procuracy request <contractor-dir> [--jira-project KEY] [--issue-type TYPE]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Creates a Jira ticket with the contractor's manifest for team review.")
		fmt.Fprintln(stderr, "The ticket IS the approval — once the team lead transitions it to")
		fmt.Fprintln(stderr, `"Approved" or "Done", run procuracy hire to provision the agent.`)
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Requires JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN environment variables.")
		return 2
	}

	dir := args[0]
	jiraProject := ""
	issueType := "Task"
	for i := 1; i < len(args); i++ {
		if args[i] == "--jira-project" && i+1 < len(args) {
			jiraProject = args[i+1]
			i++
		}
		if args[i] == "--issue-type" && i+1 < len(args) {
			issueType = args[i+1]
			i++
		}
	}

	if jiraProject == "" {
		fmt.Fprintln(stderr, "request: --jira-project is required")
		return 2
	}

	// Load the manifest.
	manifestPath := filepath.Join(dir, "procuracy.yaml")
	m, err := manifest.Load(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "request: %v\n", err)
		return 1
	}

	// Check it hasn't already been requested.
	if m.State != nil && m.State.ApprovalTicket != "" {
		fmt.Fprintf(stderr, "request: %s already has an approval ticket: %s (phase: %s)\n",
			m.Name, m.State.ApprovalTicket, m.State.Phase)
		return 1
	}

	// Build Jira config from environment.
	jiraCfg := &jira.Config{
		BaseURL: os.Getenv("JIRA_BASE_URL"),
		Email:   os.Getenv("JIRA_EMAIL"),
		Token:   os.Getenv("JIRA_API_TOKEN"),
	}
	if jiraCfg.BaseURL == "" || jiraCfg.Email == "" || jiraCfg.Token == "" {
		fmt.Fprintln(stderr, "request: JIRA_BASE_URL, JIRA_EMAIL, and JIRA_API_TOKEN must be set")
		return 1
	}

	// Read the raw manifest for the ticket description.
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "request: read manifest: %v\n", err)
		return 1
	}

	// Create the Jira ticket.
	summary := fmt.Sprintf("New agent request: %s", m.Name)
	if m.Group != "" {
		summary += fmt.Sprintf(" (group: %s)", m.Group)
	}
	ticketKey, err := jira.CreateIssue(jiraCfg, jiraProject, issueType, summary, string(rawManifest))
	if err != nil {
		fmt.Fprintf(stderr, "request: create ticket: %v\n", err)
		return 1
	}

	// Update the manifest with the state block.
	if m.State == nil {
		m.State = &manifest.State{}
	}
	m.State.Phase = manifest.StatePhaseRequested
	m.State.ApprovalTicket = ticketKey
	m.State.RequestedBy = jiraCfg.Email
	m.State.History = append(m.State.History,
		fmt.Sprintf("requested via procuracy request → %s", ticketKey))

	// Write the updated manifest back.
	updatedYAML, err := yaml.Marshal(m)
	if err != nil {
		fmt.Fprintf(stderr, "request: marshal manifest: %v\n", err)
		return 1
	}
	if err := os.WriteFile(manifestPath, updatedYAML, 0644); err != nil {
		fmt.Fprintf(stderr, "request: write manifest: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "procuracy request: created %s — %s\n", ticketKey, summary)
	fmt.Fprintf(stdout, "procuracy request: manifest updated with state.approval_ticket=%s\n", ticketKey)
	fmt.Fprintf(stdout, "\nNext steps:\n")
	fmt.Fprintf(stdout, "  1. Team lead reviews the manifest in %s\n", ticketKey)
	fmt.Fprintf(stdout, "  2. Team lead transitions the ticket to \"Approved\"\n")
	fmt.Fprintf(stdout, "  3. Run: procuracy hire %s\n", dir)
	return 0
}
