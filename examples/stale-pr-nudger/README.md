# stale-pr-nudger

A procuracy contractor that reviews stale PRs and posts summary comments to help reviewers resume. Read-only plus comments — never merges, never approves.

## Quick start

```bash
# 1. Copy this template
cp -r examples/stale-pr-nudger ./my-nudger

# 2. Edit the manifest — change the GitHub username and org
vim ./my-nudger/procuracy.yaml

# 3. Validate
procuracy validate ./my-nudger/procuracy.yaml

# 4. Run
procuracy run ./my-nudger/
```

## What it does

- Lists open PRs with no activity in the last 7 days
- Reads the diff and any existing review comments
- Posts a summary comment with: what the PR does, last activity, outstanding items, suggested next step
- Skips PRs it has already nudged

## What it cannot do

The manifest enforces `merge:none` and `write:none` — the agent physically cannot merge PRs or modify code. The audit log at `./my-nudger/audit.jsonl` records every action for verification.

## Cost

Uses `claude-sonnet-4-6` with a $2/task cap and $10/day cap. A typical run across 5-10 stale PRs costs $0.10-0.50.
