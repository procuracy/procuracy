package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/procuracy/procuracy/internal/jira"
	"github.com/procuracy/procuracy/internal/manifest"
	"gopkg.in/yaml.v3"
)

func cmdHire(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: procuracy hire <contractor-dir>")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Provisions a contractor after the approval ticket has been approved.")
		fmt.Fprintln(stderr, "The manifest must have state.approval_ticket set (via procuracy request).")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Requires JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN environment variables.")
		return 2
	}

	dir := args[0]

	// Accepted statuses for approval (case-insensitive).
	approvedStatuses := []string{"approved", "done"}
	for i := 1; i < len(args); i++ {
		if args[i] == "--approved-status" && i+1 < len(args) {
			approvedStatuses = strings.Split(args[i+1], ",")
			for j := range approvedStatuses {
				approvedStatuses[j] = strings.TrimSpace(approvedStatuses[j])
			}
			i++
		}
	}

	// Load the manifest.
	manifestPath := filepath.Join(dir, "procuracy.yaml")
	m, err := manifest.Load(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "hire: %v\n", err)
		return 1
	}

	// Check state.
	if m.State == nil || m.State.ApprovalTicket == "" {
		fmt.Fprintln(stderr, "hire: no approval ticket found in the manifest")
		fmt.Fprintln(stderr, "Run 'procuracy request' first to create an approval ticket.")
		return 1
	}
	if m.State.Phase == manifest.StatePhaseProvisioned {
		fmt.Fprintf(stdout, "hire: %s is already provisioned (ticket: %s)\n",
			m.Name, m.State.ApprovalTicket)
		return 0
	}

	// Build Jira config.
	jiraCfg := &jira.Config{
		BaseURL: os.Getenv("JIRA_BASE_URL"),
		Email:   os.Getenv("JIRA_EMAIL"),
		Token:   os.Getenv("JIRA_API_TOKEN"),
	}
	if jiraCfg.BaseURL == "" || jiraCfg.Email == "" || jiraCfg.Token == "" {
		fmt.Fprintln(stderr, "hire: JIRA_BASE_URL, JIRA_EMAIL, and JIRA_API_TOKEN must be set")
		return 1
	}

	// Check ticket status.
	ticketKey := m.State.ApprovalTicket
	status, err := jira.GetStatus(jiraCfg, ticketKey)
	if err != nil {
		fmt.Fprintf(stderr, "hire: check ticket status: %v\n", err)
		return 1
	}

	isApproved := false
	for _, s := range approvedStatuses {
		if strings.EqualFold(status, s) {
			isApproved = true
			break
		}
	}

	if !isApproved {
		fmt.Fprintf(stderr, "hire: ticket %s is not yet approved (current status: %q)\n", ticketKey, status)
		fmt.Fprintf(stderr, "The team lead needs to transition the ticket to one of: %s\n",
			strings.Join(approvedStatuses, ", "))
		return 1
	}

	// Approved — update the manifest state.
	m.State.Phase = manifest.StatePhaseProvisioned
	m.State.ApprovedBy = status // record what status triggered approval
	m.State.History = append(m.State.History,
		fmt.Sprintf("approved (ticket %s status: %s)", ticketKey, status))

	updatedYAML, err := yaml.Marshal(m)
	if err != nil {
		fmt.Fprintf(stderr, "hire: marshal manifest: %v\n", err)
		return 1
	}
	if err := os.WriteFile(manifestPath, updatedYAML, 0644); err != nil {
		fmt.Fprintf(stderr, "hire: write manifest: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "procuracy hire: %s is provisioned (approved via %s)\n", m.Name, ticketKey)
	fmt.Fprintf(stdout, "\nReady to run:\n")
	fmt.Fprintf(stdout, "  procuracy run %s\n", dir)
	fmt.Fprintf(stdout, "  procuracy watch --dir %s --jira-project ... --jira-assignee %s\n", dir, m.Name)
	return 0
}
