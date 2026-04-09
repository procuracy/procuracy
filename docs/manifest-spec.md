# `procuracy.yaml` manifest specification

**Status:** v0.1 (alpha — fields may be added; existing fields will not be removed without a major version bump)

A `procuracy.yaml` is a single declarative file that describes everything an AI contractor *is*: who it is, what it can touch, when it works, how it thinks, and how to fire it. One file → one contractor. Versioned in git, reviewed in PRs, auditable forever.

This document is the authoritative reference. The Go parser in [`internal/manifest`](../internal/manifest) is generated to match.

---

## Top-level structure

```yaml
name: aria                       # required, [a-z0-9-], unique within a procuracy workspace
display_name: "Aria — Docs Maintainer"   # optional, human-readable
description: |                   # optional, free text
  Keeps API docs in sync with code.

identity:        { ... }         # required
scopes:          { ... }         # required (may be empty {} for a no-op contractor)
triggers:        [ ... ]         # required (at least one)
runtime:         { ... }         # required
handlers:        { ... }         # required (at least one)
observability:   { ... }         # optional
termination:     { ... }         # optional but strongly recommended
```

Unknown top-level keys are a **validation error**, not a warning. The manifest is the source of truth — typos must fail loudly.

---

## `name` (string, required)

The contractor's stable identifier. Used for filesystem paths, audit log entries, and CLI invocations (`procuracy start <name>`).

- Must match `^[a-z][a-z0-9-]{1,30}$`
- Must be unique across contractors managed by the same `procuracy` workspace
- Cannot be changed without re-hiring (it is the contractor's primary key)

## `display_name` (string, optional)

Free-form human-readable name. Surfaces in Slack posts, PR descriptions, and weekly reports. Defaults to `name` if omitted.

## `description` (string, optional)

Free text, multi-line allowed. Used in `procuracy report`, in the welcome post when a contractor is hired, and as the GitHub user bio.

---

## `identity` (object, required)

Real, scoped accounts on the tools your team uses. Each field is the *desired* identity — `procuracy hire` provisions these accounts (with the operator's OAuth approval) and writes back any IDs the provider assigns.

```yaml
identity:
  email: aria@acme.com           # required if any handler sends mail
  github_username: aria-acme     # required if scopes.github is set
  slack_handle: aria             # required if scopes.slack is set
  linear_user: aria              # required if scopes.linear is set
```

Validation rule: every `scopes.<integration>` block must have a corresponding `identity.<integration>_*` field. You cannot scope an integration the contractor has no identity in.

---

## `scopes` (object, required)

A **capability declaration**. The runtime enforces these at the adapter layer — meaning if `merge:none` is declared on GitHub, the GitHub adapter constructed for this contractor physically does not expose a `MergePR` method. Capability-based, not instruction-based.

```yaml
scopes:
  github:
    - read:org/*                 # read everything in the org
    - write:org/docs/**          # path-scoped writes (glob)
    - pr:create:org/docs         # may open PRs against acme/docs
    - merge:none                 # explicit denial — overrides any wildcard
  slack:
    - post:#engineering
    - post:#aria-log
    - dm:none
  linear:
    - read:project/eng
    - comment:project/eng
    - transition:project/eng/{Todo,InProgress,InReview,Done}
```

### Scope grammar

Each scope string is `<verb>:<resource>` or `<verb>:none`.

- **Verbs** are integration-specific. The adapter publishes its supported verb set; unknown verbs fail validation. Common verbs: `read`, `write`, `comment`, `post`, `dm`, `pr:create`, `merge`, `transition`, `assign`.
- **Resources** use glob syntax: `*` matches one path segment, `**` matches any depth. `{a,b,c}` is alternation.
- **`<verb>:none`** is an explicit denial. Denials always win over grants — even a wildcard grant cannot override a `none` for the same verb.
- **Order does not matter.** Scopes are a set, not a list.

### Reserved integration keys

The v0.1 spec reserves these adapter names: `github`, `slack`, `linear`, `jira`, `notion`, `email`, `gitlab`, `bitbucket`, `discord`. Adding a new adapter is a non-breaking change; using an unreserved key is a validation error to prevent typos.

---

## `triggers` (list, required)

When the contractor wakes up. At least one trigger must be defined or the contractor will never run.

```yaml
triggers:
  - on: linear.issue.assigned
    where: assignee == 'aria'
    do: handle_ticket

  - on: github.pull_request.merged
    where: files matches 'src/api/**'
    do: review_doc_drift

  - on: schedule
    cron: "0 9 * * 1-5"
    do: daily_standup
```

Each trigger has:

- **`on`** (required): event identifier, in `<integration>.<resource>.<action>` form, OR the literal string `schedule`.
- **`where`** (optional): a filter expression evaluated against the event payload. v0.1 supports a small expression language: `==`, `!=`, `&&`, `||`, `matches` (glob), `in [...]`, field access via `.`. No function calls in v0.1.
- **`cron`** (required iff `on: schedule`): standard 5-field cron, evaluated in the workspace's local timezone.
- **`do`** (required): the name of a handler defined in the `handlers` block. Validation errors if the name is undefined.

A trigger fires the named handler with the event payload as input. Handlers are not allowed to fire other handlers directly — chaining happens through real events, not in-process calls, so the audit log captures every transition.

---

## `runtime` (object, required)

How the contractor thinks and what budget it has.

```yaml
runtime:
  engine: claude-code            # required: claude-code | openhands | openai-assistants | custom
  model: claude-opus-4-6         # required for engines that take a model
  workspace: /var/procuracy/aria  # required, absolute path; created on hire
  cost_limit_daily_usd: 50       # required, > 0
  cost_limit_per_task_usd: 5     # required, > 0, must be <= cost_limit_daily_usd
  timeout_per_task_seconds: 1800 # optional, default 1800
  max_concurrent_tasks: 1        # optional, default 1
```

### Cost limits are enforced, not advisory

Every LLM API call goes through `procuracy`'s cost interceptor. If a call would push the running total over `cost_limit_daily_usd` *or* `cost_limit_per_task_usd`, the call is **blocked** before it leaves the process. Cost runaways are impossible by construction. (See [`docs/security.md`](security.md) §2.)

### Engine values

- **`claude-code`** — spawns a Claude Code subprocess in `workspace`, scoped to the contractor's tools. Default engine for v0.1.
- **`openhands`** — runs an OpenHands agent loop. Stub in v0.1.
- **`openai-assistants`** — uses the OpenAI Assistants API. Stub in v0.1.
- **`custom`** — invokes a user-supplied binary; see [`docs/custom-engine.md`](custom-engine.md). Stub in v0.1.

---

## `handlers` (object, required)

Named units of work. Triggers reference handlers by name. At least one handler must be defined.

```yaml
handlers:
  handle_ticket:
    type: claude_code            # required: claude_code | script
    prompt: prompts/handle_ticket.md   # required for type=claude_code, path relative to manifest
  build_release_notes:
    type: script
    command: ["./scripts/release_notes.sh"]   # required for type=script
```

Handler names must match `^[a-z][a-z0-9_]*$`. Every name referenced from `triggers[*].do` must exist in this block; every name in this block must be referenced by at least one trigger (unused handlers are a validation error to keep manifests honest).

---

## `observability` (object, optional)

Where humans watch the work.

```yaml
observability:
  audit_channel: "#aria-log"     # optional Slack channel for real-time audit posts
  metrics: prometheus://localhost:9090   # optional metrics sink
  audit_log_path: ./contractors/aria/audit.jsonl   # default if omitted
```

The local JSONL audit log is **always written**, regardless of this block. The block only configures *additional* mirrors. (See [`docs/audit-log.md`](audit-log.md) for the on-disk format.)

---

## `termination` (object, optional but strongly recommended)

How `procuracy fire` undoes everything.

```yaml
termination:
  on_kill_signal:
    - revoke: github_token
    - revoke: slack_token
    - revoke: linear_token
    - archive_accounts: true
    - notify: "#engineering"
```

Each step runs in declared order. If any step fails, `procuracy fire` reports the failure but continues with the remaining steps — the goal is best-effort revocation, not transactional rollback. The CLI exits non-zero if any step failed so operators can investigate.

If `termination` is omitted, `procuracy fire` falls back to revoking every token and archiving every account it can find for the contractor's identities — safer than doing nothing, but less precise than a declared list.

---

## Validation order

When `procuracy` loads a manifest it runs these checks in order. The first failing check produces a hard error; later checks are not run.

1. **Parse** — valid YAML, no unknown top-level keys.
2. **Required fields** — `name`, `identity`, `scopes`, `triggers`, `runtime`, `handlers` are present.
3. **Field shape** — `name` matches the regex, cron strings parse, paths are absolute where required, costs are positive.
4. **Cross-references** — every `triggers[*].do` resolves to a handler; every `scopes.<integration>` has a matching `identity` field; every handler is referenced.
5. **Adapter validation** — each scope verb is recognized by its adapter, each trigger event identifier is recognized.

Stages 1–4 run in pure Go and require no network. Stage 5 requires the adapter registry but no live credentials.

---

## What is *not* in v0.1

Deliberately omitted to keep the spec small and forkable:

- **Multi-contractor coordination.** A contractor cannot reference or invoke another. If you want pipelines, model them as triggers on real events.
- **Dynamic scope grants.** Scopes are static — you cannot widen a contractor's permissions at runtime. Edit the manifest, re-run `procuracy update`, get the diff in your git history.
- **Secret management.** Tokens live in the workspace token store (`<workspace>/.tokens`, mode 0600). Integration with Vault/SOPS/etc. is left to the operator for v0.1.
- **Templating / inheritance.** No `extends:` field. Templates are full manifests you copy and edit. This might change in v0.2 if real usage demands it.
