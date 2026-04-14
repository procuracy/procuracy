# Roll out procuracy in your org

**Time:** 30 minutes for the first agent. 5 minutes for each additional.

This guide walks through deploying procuracy-managed AI agents across your team's repos. It's progressive — start with one agent, add more when it works, layer in policy when you need consistency.

---

## Prerequisites

- **Go 1.25+** — to install procuracy
- **Claude Code CLI** (`claude` on PATH) — the agent runtime
- **Anthropic API key** — set `ANTHROPIC_API_KEY` in your environment
- **A Slack incoming webhook** (optional) — for team notifications
- **Jira API token** (optional) — for ticket comments

## Step 1: Install procuracy (2 minutes)

```bash
go install github.com/procuracy/procuracy/cmd/procuracy@latest
procuracy version
```

## Step 2: Try the demo (2 minutes)

```bash
procuracy demo
```

Follow the printed instructions — validate a manifest, verify an audit log, corrupt a byte and see the chain break. This builds intuition for what procuracy does before you deploy anything real.

## Step 3: Fork a template (5 minutes)

Pick the template that matches your first use case:

| Template | Best for | Risk |
|---|---|---|
| [stale-pr-nudger](../examples/stale-pr-nudger/) | Teams with PRs that sit for days | Very low (read + comment only) |
| [docs-maintainer](../examples/docs-maintainer/) | Teams with docs that drift from code | Low (writes only to docs/) |
| [issue-triager](../examples/issue-triager/) | Teams drowning in untriaged issues | Low (read + label + comment) |
| [dependabot-merger](../examples/dependabot-merger/) | Teams with a backlog of dep bumps | Low (merges only patch/minor with green CI) |
| [release-notes-writer](../examples/release-notes-writer/) | Teams that skip changelogs | Low (writes only CHANGELOG.md) |

```bash
cp -r examples/stale-pr-nudger ./agents/nudger
```

Edit `./agents/nudger/procuracy.yaml`:
- Change `github_username` to your bot's GitHub username
- Change the org/repo references in scopes to match your repos
- Set your Slack webhook URL (or remove the notifications block)

```bash
procuracy validate ./agents/nudger/procuracy.yaml
# → ok: stale-pr-nudger (1 trigger(s), 1 handler(s))
```

## Step 4: Run it (1 minute)

```bash
export SLACK_WEBHOOK_URL="https://hooks.slack.com/services/T.../B.../xxx"
procuracy run ./agents/nudger/
```

Your team sees the agent working in Slack. The audit log is at `./agents/nudger/audit.jsonl`. Verify it any time:

```bash
procuracy verify ./agents/nudger/audit.jsonl
```

## Step 5: Add more agents (5 minutes each)

Repeat steps 3–4 with different templates. Each agent gets its own directory:

```
agents/
  nudger/
    procuracy.yaml
    prompts/nudge.md
    audit.jsonl
  docs-bot/
    procuracy.yaml
    prompts/sync_docs.md
    audit.jsonl
  triager/
    procuracy.yaml
    prompts/triage.md
    audit.jsonl
```

## Step 6: Add a groups.yaml for consistent policy (10 minutes)

Once you have 3+ agents, you'll want consistent scoping and cost limits. Create a `groups.yaml` in your agents directory:

```yaml
# agents/groups.yaml
bot-readonly:
  description: "Read everything, write nothing, never merge."
  scopes:
    github:
      - read:org/*
      - merge:none
      - write:none
      - admin:none
  cost_limit_daily_usd: 15
  cost_limit_per_task_usd: 2
  notifications:
    slack_webhook: ${SLACK_WEBHOOK_URL}

bot-docs-write:
  description: "Read everything, write only docs/, never merge."
  scopes:
    github:
      - read:org/*
      - write:org/*/docs/**
      - pr:org/*
      - merge:none
      - admin:none
  cost_limit_daily_usd: 25
  cost_limit_per_task_usd: 3
  notifications:
    slack_webhook: ${SLACK_WEBHOOK_URL}
```

Then simplify each contractor manifest to reference the group:

```yaml
# agents/nudger/procuracy.yaml
name: nudger
group: bot-readonly          # inherits scopes, costs, notifications
identity:
  github_username: nudger-bot
triggers:
  - on: schedule
    cron: "0 9 * * 1-5"
    do: nudge_stale_prs
runtime:
  engine: claude-code
  workspace: /tmp/procuracy/nudger
handlers:
  nudge_stale_prs:
    type: claude_code
    prompt: prompts/nudge.md
```

**Changing a group changes every agent that uses it.** One PR, one review, all agents updated.

## Step 7: Version everything in git

Your agents directory is now infrastructure-as-code. Commit it:

```bash
git add agents/
git commit -m "Add procuracy agents: nudger, docs-bot, triager"
```

New agent? Open a PR. The manifest diff is the security review:

```diff
+ name: release-bot
+ group: bot-docs-write
+ scopes:
+   github:
+     - write:org/*/CHANGELOG.md    # override: narrower than the group
```

The reviewer can see exactly what the agent can touch, what it costs, and what it can't do. The PR IS the approval.

## Step 8: Connect to Jira (optional, 5 minutes)

If your team uses Jira, add Jira notification config to your group or manifest:

```yaml
notifications:
  slack_webhook: ${SLACK_WEBHOOK_URL}
  jira_base_url: ${JIRA_BASE_URL}
  jira_email: ${JIRA_EMAIL}
  jira_token: ${JIRA_API_TOKEN}
```

Run with a ticket reference:

```bash
procuracy run ./agents/nudger/ --jira-ticket PROJ-456
```

The agent's results are posted as a comment on the Jira ticket automatically.

## Step 9: Verify audit logs regularly

Set up a cron job or CI step that verifies all audit logs:

```bash
# verify-audits.sh — run daily in CI
for log in agents/*/audit.jsonl; do
  procuracy verify "$log" || echo "TAMPERED: $log"
done
```

If any log has been modified, the chain breaks and the script reports it. This is your compliance story: "every agent action is recorded in a tamper-evident log that we verify daily."

---

## Security checklist for your team

Before deploying agents to production repos:

- [ ] Every agent has `merge:none` unless it explicitly needs to merge (only dependabot-merger should)
- [ ] Every agent has `admin:none`
- [ ] Cost limits are set (`cost_limit_per_task_usd` prevents runaway spending on a single task)
- [ ] Notifications are configured (the team should see what agents are doing in Slack)
- [ ] Audit logs are verified regularly (daily CI job or cron)
- [ ] Manifests are versioned in git and changes go through PR review
- [ ] Groups.yaml is owned by the team lead / security, not individual contributors
- [ ] `ANTHROPIC_API_KEY` and `JIRA_API_TOKEN` are in environment variables, not in manifests

## FAQ

**Q: Can an agent bypass its scopes via prompt injection?**
A: Tool-level scoping (which tools exist) is structural — no prompt can add a tool that wasn't granted. Verb-level scoping within tools (e.g., "don't merge") is instruction-based and theoretically defeatable. The audit log is the second line of defense: if an agent does something it shouldn't, the log records it with hash-chain integrity. See the README's trust model table for details.

**Q: What if an agent costs too much?**
A: `cost_limit_per_task_usd` is enforced by the agent CLI's `--max-budget-usd` flag. Over-budget calls are blocked, not logged. Set this conservatively ($2-5 for most tasks) and adjust based on the audit log's cost entries.

**Q: How do I fire an agent?**
A: Delete its directory and revoke its GitHub/Slack credentials. `procuracy fire` (one-command revocation) is coming in v0.2.

**Q: Can two agents run at the same time?**
A: Yes. Each agent has its own directory, audit log, and workspace. They don't share state. Run them in parallel with separate `procuracy run` commands.

**Q: What if I want to test an agent before deploying?**
A: Run it on a fork or a dev repo first. The manifest scopes are per-org/repo, so you can create a testing manifest that points to a sandbox repo and swap it for the real one when ready.
