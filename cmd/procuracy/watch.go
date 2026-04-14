package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/procuracy/procuracy/internal/jira"
	"github.com/procuracy/procuracy/internal/notify"
)

func cmdWatch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dir := fs.String("dir", ".", "contractor directory containing procuracy.yaml")
	project := fs.String("jira-project", "", "Jira project key (e.g., PROJ)")
	assignee := fs.String("jira-assignee", "", "Jira assignee name for the agent")
	pollInterval := fs.Duration("poll", 5*time.Minute, "polling interval (e.g., 5m, 1m, 30s)")
	pickupStatus := fs.String("pickup-status", "To Do", "Jira status to pick up tickets from")
	doneStatus := fs.String("done-status", "Done", "Jira status to transition to on success")
	failStatus := fs.String("fail-status", "Blocked", "Jira status to transition to on failure")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: procuracy watch [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Polls Jira for tickets assigned to the agent and runs procuracy run")
		fmt.Fprintln(stderr, "for each new ticket. Results are posted as comments and the ticket")
		fmt.Fprintln(stderr, "is transitioned to Done or Blocked.")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Requires JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN environment variables.")
		fmt.Fprintln(stderr, "")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == "" || *assignee == "" {
		fmt.Fprintln(stderr, "watch: --jira-project and --jira-assignee are required")
		fs.Usage()
		return 2
	}

	// Build Jira config from environment.
	jiraCfg := &jira.Config{
		BaseURL: os.Getenv("JIRA_BASE_URL"),
		Email:   os.Getenv("JIRA_EMAIL"),
		Token:   os.Getenv("JIRA_API_TOKEN"),
	}
	if jiraCfg.BaseURL == "" || jiraCfg.Email == "" || jiraCfg.Token == "" {
		fmt.Fprintln(stderr, "watch: JIRA_BASE_URL, JIRA_EMAIL, and JIRA_API_TOKEN must be set")
		return 1
	}

	// Set up context with signal handling.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(stderr, "\nprocuracy watch: shutting down...")
		cancel()
	}()

	fmt.Fprintf(stdout, "procuracy watch: polling %s for %s tickets assigned to %s every %s\n",
		*project, *pickupStatus, *assignee, *pollInterval)

	// Track which tickets we've already processed to avoid re-runs.
	processed := map[string]bool{}

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "procuracy watch: stopped")
			return 0
		default:
		}

		issues, err := jira.FindAssigned(jiraCfg, *project, *assignee, *pickupStatus)
		if err != nil {
			fmt.Fprintf(stderr, "watch: jira search error: %v\n", err)
		} else {
			for _, issue := range issues {
				if processed[issue.Key] {
					continue
				}
				processed[issue.Key] = true

				fmt.Fprintf(stdout, "procuracy watch: picked up %s — %s\n", issue.Key, issue.Summary)

				// Transition to In Progress.
				if err := jira.Transition(jiraCfg, issue.Key, "In Progress"); err != nil {
					fmt.Fprintf(stderr, "watch: transition %s to In Progress: %v\n", issue.Key, err)
				}

				// Build the prompt context with ticket info.
				promptCtx := fmt.Sprintf("Jira ticket: %s\nSummary: %s\nDescription: %s",
					issue.Key, issue.Summary, issue.Desc)

				// Run procuracy for this ticket.
				runArgs := []string{*dir, "--jira-ticket", issue.Key}
				code := cmdRun(runArgs, stdout, stderr)

				// Post result comment.
				notifyCfg := &notify.JiraConfig{
					BaseURL: jiraCfg.BaseURL,
					Email:   jiraCfg.Email,
					Token:   jiraCfg.Token,
				}
				if code == 0 {
					notify.JiraComment(notifyCfg, issue.Key, notify.Event{
						Type:       "complete",
						Contractor: *assignee,
					})
					if err := jira.Transition(jiraCfg, issue.Key, *doneStatus); err != nil {
						fmt.Fprintf(stderr, "watch: transition %s to %s: %v\n", issue.Key, *doneStatus, err)
					}
					fmt.Fprintf(stdout, "procuracy watch: %s → %s\n", issue.Key, *doneStatus)
				} else {
					notify.JiraComment(notifyCfg, issue.Key, notify.Event{
						Type:       "fail",
						Contractor: *assignee,
						Error:      "agent run failed (see audit log)",
					})
					if err := jira.Transition(jiraCfg, issue.Key, *failStatus); err != nil {
						fmt.Fprintf(stderr, "watch: transition %s to %s: %v\n", issue.Key, *failStatus, err)
					}
					fmt.Fprintf(stdout, "procuracy watch: %s → %s\n", issue.Key, *failStatus)
				}

				// Suppress unused variable warning — promptCtx will be used
				// when we inject ticket context into the handler prompt.
				_ = promptCtx
			}
		}

		// Wait for next poll or shutdown.
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "procuracy watch: stopped")
			return 0
		case <-time.After(*pollInterval):
		}
	}
}
