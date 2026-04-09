# Enterprise provisioning: the gap between the README and reality

**Status:** design note. Captures procuracy's official position on the gap between its current single-operator model and the multi-actor, IdP-managed reality of provisioning at most companies. **No code in this document is implemented yet.** This is the v0.2+ trajectory; v0.1 ships the simpler model and validates the pipeline end-to-end first.

If you are evaluating procuracy for use inside a company larger than ~30 people, **read this document before assuming the README's `procuracy hire` flow will work for you**. It will not, yet. We are not pretending otherwise, and we are designing procuracy so that the work to support you is mostly additive, not breaking.

---

## 1. The model the README describes

The current README assumes a world where:

- **One operator** runs `procuracy hire aria` from their laptop
- That operator holds **OAuth admin tokens** for every integration in the manifest
- procuracy **calls each adapter's API directly** to create accounts
- Provisioning, approval, and execution all happen **in one place, by one person, in one shell session**

This works great for:

- Solo founders and small teams (<10 people) where the founder is also the IT admin
- Open-source maintainers running bots on their own repos
- Skunkworks experiments inside larger orgs, where the operator is a tech lead with admin rights to a sandbox
- Hobby projects, demos, conference talks

This is roughly **5% of the addressable market**. It is also the right place to start, because it is the simplest possible deployment shape and proves the manifest → capability → adapter → engine → audit pipeline works end-to-end. **v0.1 ships exactly this model.**

## 2. The model real companies have

This is what actually happens when someone wants to hire `aria` at a typical 200-person SaaS company:

| # | Actor | Tool | Action |
|---|---|---|---|
| 1 | **Eng manager** | Jira / intake form | Files a "new bot account" request — name, purpose, scopes, duration, business justification, expected cost cap |
| 2 | **Security / compliance** | Jira workflow | Reviews the requested scopes against the data classification of what the bot will touch. Approves, pushes back, or escalates. |
| 3 | **IT** | Google Workspace / Microsoft 365 admin | Creates `aria@company.com`, often via Okta/JumpCloud SCIM, sometimes manually |
| 4 | **IT** | Identity provider (Okta, Azure AD, Google Workspace, JumpCloud) | Creates the IdP user, assigns them to one or more groups (e.g. `bots-readonly`, `bots-docs-write`) |
| 5 | **SCIM** | Automatic | The IdP cascades the new user into GitHub Enterprise, Slack, Jira, Notion, Datadog, etc. The user appears in each tool with the *group's* permissions. **No manual per-tool account creation.** |
| 6 | **IT / cloud team** | AWS IAM Identity Center | Maps the IdP group → AWS permission set → role in the `dev`, `uat`, `prod` accounts. Typically `dev:read`, `uat:none`, `prod:none` for a new AI bot until proven. |
| 7 | **Per subprocessor** | Stripe / Datadog / PagerDuty / Sentry / etc. admin | Some via SCIM (if the company pays the [SSO tax](https://sso.tax)), some manual. Coverage is uneven and depends on which SaaS the product depends on. |
| 8 | **Eng manager** | Jira | Tickets get assigned to `aria` in Jira; she works them; output is reviewed; the trail lives in Jira comments. |
| 9 | **Eventually** | Security review or offboarding | `aria` is fired — IdP user disabled → SCIM cascades the deactivation everywhere → AWS access revoked. Done. |

**The single most important property of this flow:** the person *requesting* the bot, the person *approving* it, and the person *provisioning* it are **three different people**, and that is a *feature*, not friction. It is the separation of duties that makes the bot adoption pass a security review. procuracy's job is to make the separation cheap, not to bypass it.

## 3. Why the gap exists

procuracy was designed manifest-first: figure out what a contractor *is* declaratively, then figure out how to *make* one. The "make one" part was sketched assuming the simplest possible deployment — one operator, direct OAuth — because that is the path with the fewest moving parts and the cleanest demo.

This was the right call for v0.1. It gets the manifest spec, parser, capability layer, audit log, and at least one adapter shipped without also dragging in identity provider integration, approval workflows, multi-account AWS, and SCIM provisioning loops on the same release.

But it means procuracy as documented today **does not fit how real enterprises provision anything**. The point of this doc is to (a) name that gap publicly, (b) explain why we're not pretending it's not there, and (c) lay out the v0.2+ design so anyone evaluating procuracy for enterprise use can see the trajectory.

## 4. The concrete gaps

| # | Gap | Why it matters | Status in v0.1 |
|---|---|---|---|
| 1 | **No request/approval lifecycle** | Real provisioning has 3+ actors with audit between them. The manifest needs to travel through `drafted → requested → approved → provisioned → running → fired` states with each transition logged. | Not modeled. |
| 2 | **No identity provider integration** | In SSO-managed orgs, GitHub/Slack/Jira users are not created by procuracy — they are created by the IdP via SCIM. procuracy should orchestrate the IdP, not the downstream tools. | No `identity.idp` block; no Okta/AzureAD/Google adapters; no concept of group membership. |
| 3 | **No group-based scoping** | In SSO worlds, permissions are per-group, not per-user. The right model is "this contractor goes in *this group* which has *these scopes*." | The `scopes` block is per-user/per-adapter; cannot reference IdP groups. |
| 4 | **Jira is a stub** | "All tasks will be defined in Jira" is the actual workflow at most companies. Jira has to be both a *trigger source* and an *audit mirror*, not an aspirational reserved keyword. | `jira` is in `reservedIntegrations` but no adapter, no trigger event types defined. |
| 5 | **No AWS adapter** | AWS is not even in the reserved integration list. Multi-account orgs need explicit `aws:dev:role:X`, `aws:prod:none` style scoping with regions, services, and STS AssumeRole. | Not in the spec. |
| 6 | **No subprocessor extension point** | Each company has different SaaS — Stripe, Datadog, PagerDuty, Notion, Snowflake, Sentry, Linear, Vercel, Cloudflare. The reserved-integrations list cannot enumerate them all up front. | The list is closed; adapters cannot be added without modifying procuracy core. |
| 7 | **No separation of "who runs procuracy" from "who has the credentials"** | Today the operator must hold all the OAuth tokens; in real orgs they don't. There is no way for an engineer to draft a manifest and hand it off to IT to execute. | No notion of remote/delegated execution; no signed manifest hand-off; no credential broker. |
| 8 | **No SCIM-aware termination** | `procuracy fire` revokes via direct API. In IdP-managed worlds, "fire" should disable the IdP user and let SCIM cascade everywhere automatically. | `termination.on_kill_signal` does not model "deactivate IdP user, let SCIM do the rest." |
| 9 | **No data classification awareness** | Security review hinges on "what data can this bot touch" — and that is a function of the *resources* in scope, not just the verbs. | Scopes are verbs+globs; nothing connects them to a classification taxonomy (PII, PCI, customer-data, internal-only). |

## 5. The v0.2+ design

This section sketches how procuracy *should* support enterprise provisioning. None of it is implemented yet. Each subsection includes manifest field proposals so the spec evolution is concrete.

### 5.1. Three-actor model with manifest state

Add a `state:` block, written by procuracy itself, never by humans. It tracks where the manifest is in its provisioning lifecycle and who has touched it:

```yaml
state:
  phase: draft | requested | approved | provisioned | running | paused | fired
  requested_by: alice@company.com
  approved_by: bob@company.com
  provisioned_by: it-admin@company.com
  approval_ticket: COMPANY-1234
  signature: "ed25519:..."          # signed by approver, verified by provisioner
  history:
    - 2026-04-09T10:00Z drafted by alice@company.com
    - 2026-04-09T10:05Z requested via procuracy request → COMPANY-1234
    - 2026-04-09T11:30Z approved by bob@company.com (security review)
    - 2026-04-09T11:35Z provisioned by it-admin@company.com
```

New CLI commands to drive the state machine:

| Command | Who runs it | What it does |
|---|---|---|
| `procuracy request ./aria/` | Requester (eng manager) | Validates the manifest, files a Jira ticket in the configured request queue, transitions state `draft → requested`. |
| `procuracy approve aria --ticket COMPANY-1234` | Approver (security) | Cryptographically signs the manifest so the provisioner can verify it was not tampered with after approval. State `requested → approved`. |
| `procuracy hire ./aria/` | Provisioner (IT) | Refuses to run unless state is `approved` and the signature verifies. Runs the actual provisioning. State `approved → provisioned → running`. |
| `procuracy reject aria --ticket COMPANY-1234 --reason "..."` | Approver | Rejects with a written reason; state `requested → draft` with the reason in history. |

The signed manifest is the trust artifact between the approver and the provisioner — even if the provisioner is hostile or compromised, they cannot widen scopes after approval without breaking the signature.

### 5.2. IdP-first identity model

Replace the current `identity:` block with two modes:

```yaml
identity:
  mode: idp-managed                        # or: direct (today's model, kept for solo/OSS use)
  idp: okta                                # or: azure-ad, google-workspace, jumpcloud
  email: aria@company.com                  # the only thing procuracy "creates" — via the IdP
  groups:                                  # IdP groups; procuracy assigns the new user to these
    - bots-docs-write
    - bots-jira-eng
    - bots-aws-dev-readonly
```

In `idp-managed` mode procuracy:

1. Files a request with IT to create the IdP user (or, if procuracy has IdP admin credentials, creates it directly).
2. Assigns the new user to the declared groups.
3. Waits for SCIM to cascade into GitHub, Slack, Jira, AWS, etc. (timeouts and retries are part of the adapter contract).
4. Verifies each downstream tool sees the user with the expected effective permissions — a *drift check*. If GitHub's effective permissions diverge from what the manifest declares, that is a hard failure.
5. **Audits the cascade itself.** Every downstream provisioning event becomes an entry in procuracy's audit log, even if it was actually performed by SCIM rather than by procuracy directly.

The contract becomes: **the IdP is the source of truth for identity; procuracy is the source of truth for *intent and audit*.**

`mode: direct` remains as today's behavior — direct OAuth, no IdP — for solo/OSS use cases. The two modes are not deployment fragments to be merged; they are first-class alternatives, and the manifest declares which it is using.

### 5.3. Group-based scopes via a separate `groups.yaml`

Companies have a finite set of reusable bot personas. Define them once, in a repo-versioned `groups.yaml`, owned by security and IT:

```yaml
# groups.yaml — defined once by security/IT, versioned in git, reviewed in PRs
bots-docs-write:
  description: "Read all repos, write only to docs/ trees, never merge."
  data_classification: internal
  github:
    - read:org/*
    - write:org/*/docs/**
    - merge:none
  jira:
    - read:project/*
    - comment:project/eng

bots-aws-dev-readonly:
  description: "Read-only access to dev account, no UAT, no prod."
  data_classification: internal
  aws:
    - dev:role/bot-readonly
    - uat:none
    - prod:none

bots-jira-eng:
  description: "Engineering ticket triage and transitions."
  data_classification: internal
  jira:
    - read:project/eng
    - comment:project/eng
    - transition:project/eng/{Todo,InProgress,InReview,Done}
```

Individual contractor manifests then reference groups by name and stop declaring scopes inline:

```yaml
identity:
  mode: idp-managed
  idp: okta
  email: aria@company.com
  groups:
    - bots-docs-write
    - bots-jira-eng
```

Reviewing a new contractor PR becomes a 30-second decision: *did the requester pick the right groups?* Reviewing the *groups themselves* is a separate, infrequent, security-team-owned process. This is exactly how mature IAM systems work, and it scales to hundreds of bots without a permission-explosion problem.

### 5.4. Adapter registration as a real extension point

Open up the closed `reservedIntegrations` list. Adapters become packages:

```
internal/adapters/{github,slack,jira,aws,okta,azure-ad,google,...}/    ← shipped with procuracy core
~/.procuracy/adapters/{custom-stripe,custom-datadog,custom-snowflake,...}/  ← user-installed
```

Each adapter ships a manifest of its own that the parser loads at startup:

```yaml
# internal/adapters/aws/adapter.yaml
name: aws
version: 1.0.0
verbs:
  - read
  - write
  - admin
  - none
resource_pattern: "(dev|uat|prod):role/[a-zA-Z0-9_-]+"
identity_field: aws_iam_user            # or: aws_sso_user
required_credentials: [aws_admin_role_arn]
supports_idp: true
scim_endpoint: aws-iam-identity-center
```

The parser loads adapter manifests from disk at startup and *that* becomes the validation source instead of the hard-coded Go map. Adding AWS support is then "drop in an adapter package, ship a release" not "modify the spec."

This is how Terraform providers, Vault auth methods, and Backstage plugins work. It is the only sustainable model for "any subprocessor depending on the product."

A community registry — `registry.procuracy.dev/adapters/` — lists known adapters with their maintainers, version, and audit status. Companies can pin third-party adapters in their `procuracy.yaml`:

```yaml
adapters:
  - source: github.com/procuracy/adapter-jira       @ v1.2.0
  - source: github.com/procuracy/adapter-aws        @ v0.4.0
  - source: github.com/acme-corp/adapter-internal-tool @ main   # private, in-house adapter
```

### 5.5. Jira as a tier-1 adapter

Given that **all tasks are defined in Jira** at most companies, Jira is not a stub — it is the *first* adapter to build properly, ahead of GitHub, for any enterprise-shaped customer.

Jira is four things at once:

1. **A trigger source.** New trigger event identifiers:
   - `jira.issue.assigned` — fires when a ticket is assigned to the contractor
   - `jira.issue.transitioned` — fires when a ticket changes state (e.g., moved to `In Review`)
   - `jira.comment.added` — fires when a comment mentions the contractor
   - `jira.epic.created` — fires when a new epic appears in a watched project

2. **An audit mirror.** Every contractor action gets a Jira comment posted on the ticket the action was working on. The Jira ticket becomes the human-facing audit trail; the JSONL log is the machine-readable archive.

3. **A request queue.** `procuracy request` files Jira tickets in a configured project (e.g., `IT-BOTS`) using a fixed issue type and template. Approvers see them as normal tickets in their normal queue.

4. **A failure escalation channel.** Provisioning failures, approval timeouts, budget exhaustion, and capability drift all create Jira tickets in the right queue automatically. There is no "check the procuracy logs" handoff — failures appear in the same place as everything else operations cares about.

### 5.6. AWS as multi-account from day one

AWS scopes look nothing like GitHub scopes. They need to model accounts, roles, regions, and services:

```yaml
aws:
  - dev:role/bot-readonly                   # role assumption in the dev account
  - uat:role/bot-deployer                   # role assumption in uat
  - prod:none                               # explicit denial — overrides any wildcard
  - regions: [us-east-1, us-west-2]         # region allowlist
  - services: [s3, dynamodb, lambda]        # service-level allowlist
```

The AWS adapter is constructed to use STS AssumeRole, scoped to the declared role+region+service set. The underlying credential is the operator's IAM Identity Center session — **never long-lived access keys**. If long-lived keys appear in the manifest or config, the adapter refuses to construct.

Cross-account scoping is enforced by the adapter at construction time exactly like GitHub `merge:none`: the adapter built for a contractor with `prod:none` literally has no method that can target the prod account, regardless of what the LLM tries to call.

### 5.7. SCIM-aware termination

`procuracy fire` in IdP-managed mode does not iterate adapters and call revoke endpoints. It does this:

```yaml
termination:
  on_kill_signal:
    - idp_disable                          # disable the IdP user
    - wait_for_scim_cascade: 60s           # poll until downstream tools show user disabled
    - verify_drift                         # verify each adapter shows the user as deactivated
    - archive_jira_tickets                 # archive any tickets the user is assigned to
    - notify: "#it-bots"                   # post to Slack
```

The cascade is the contract: disable at the IdP, let the existing identity infrastructure do its job, verify the result, log everything. This is dramatically simpler and more auditable than fanning out direct API calls to revoke each token individually, because it leverages the company's already-trusted SCIM provisioning loop.

### 5.8. Data classification as a first-class field

Every group in `groups.yaml` declares the data classification it grants access to:

```yaml
bots-customer-data-readonly:
  data_classification: customer-pii          # or: customer-pii-and-pci, internal, public
  ...
```

procuracy then enforces:

- **No bot can be assigned to a group whose classification exceeds the bot's declared maximum.** (Defined in the contractor manifest.)
- **The audit log records the classification of every action**, so security can ask "which bots touched PII data this quarter" with a single grep.
- **Approvers see the classification in the request ticket**, so they don't have to mentally compute it from glob patterns.

This costs ~1 field in the spec and turns scope review from "decoder ring" into "skim a label."

## 6. What v0.1 will and will not include

### Will be in v0.1

- The current single-operator, direct-OAuth model — works as the README describes
- Manifest spec, parser, validator, capability layer, audit log, GitHub adapter, one template
- **Two new spec fields** added now to bake in the v0.2 trajectory without implementing it:
  - `identity.mode` — accepts `direct` (today's behavior) and `idp-managed` (validates but errors with "not implemented in v0.1; see docs/enterprise-provisioning.md"). Adding this *now* is what prevents a breaking change in v0.2.
  - `state` — top-level block, currently optional and ignored by the runtime, but defined in the spec so v0.2's request/approve/provision flow has a place to land.
- **Adapter registration mechanism opened up** so adding adapters in v0.2 is dropping in a package, not modifying the spec.
- **This document** — `docs/enterprise-provisioning.md`, linked from the README's Status section.

### Will NOT be in v0.1

- IdP integration of any kind (Okta, Azure AD, Google Workspace, JumpCloud)
- The `procuracy request` / `procuracy approve` flow
- Group-based scoping or `groups.yaml`
- Jira adapter (event triggers, audit mirroring, request queue, escalation)
- AWS adapter (multi-account, IAM Identity Center, STS AssumeRole)
- SCIM-aware termination
- Data classification field
- Any third-party adapter from outside the procuracy core

These are the v0.2 release vehicle. Splitting the work this way means v0.1 can ship in weeks rather than months, and the enterprise piece can ship as a focused follow-up rather than a never-ending rolling release.

## 7. What changes between v0.1 and v0.2 are *not* breaking

This is the design constraint that determines whether the v0.1 → v0.2 path is painful or trivial. Listed in priority order:

1. **`identity.mode: direct` continues to work unchanged.** Existing v0.1 manifests do not need to add an `idp` field, do not need to add `groups`, do not need to add a `state` block. They continue to validate and run.
2. **The `scopes:` block continues to accept inline scope strings.** Group-based scoping is *additive* — manifests can opt into it but are not required to.
3. **The reserved integration list continues to recognize today's adapters under their current names.** Adding new adapters via the registration mechanism does not rename or remove `github`, `slack`, `linear`.
4. **The audit log JSONL format remains backwards-compatible.** v0.2 may add new entry types but does not change existing ones.

If we land any v0.2 feature that violates one of these properties, that's a v1.0 release, not a v0.2 release.

## 8. Open questions

These are the design decisions still in flight. Comments, issues, and PRs are welcome.

- **Should the manifest signature in `state.signature` be Sigstore / cosign-based, or a simpler ed25519 keypair held by the approver?** Sigstore is more enterprise-friendly (no key management) but adds a dependency on a hosted transparency log, which conflicts with the no-phone-home property.
- **Should `groups.yaml` live in the same repo as contractor manifests, or in a separate "policy" repo?** Separate repo is cleaner for separation of duties (security owns one repo, eng owns the other) but adds a cross-repo reference that has to be resolved at validation time.
- **How does `procuracy request` know which Jira project / issue type / approver group to file against?** Probably a per-repo `procuracy/config.yaml` that the operator sets up once. But this introduces a configuration layer above the per-contractor manifest.
- **What does `procuracy update` look like in IdP-managed mode?** Hot-reloading scopes when the actual scope source is the IdP groups means we may need to write back to the IdP, which has a much higher blast radius than today's local-only update.
- **Should procuracy ship a minimal IdP of its own** (single-binary OIDC + SCIM provisioner) for organizations that have not yet adopted a real one? This is a substantial scope expansion but might be the right answer for the long tail of small companies that want enterprise-grade flow without buying Okta.
- **Where does cost accounting live in a multi-actor world?** Today the operator's API key pays. In a real enterprise, cost should bill against the requester's team budget, not the provisioner's. This needs a billing dimension in the manifest and/or the IdP integration.

If you have opinions on any of these, please open a GitHub Discussion. The v0.2 design is still moldable.

---

**Summary in one paragraph:** procuracy v0.1 ships the simple single-operator model the README describes — fine for solo founders, OSS maintainers, and skunkworks. Real enterprise adoption requires a three-actor request/approve/provision flow, IdP-first identity management with SCIM-cascaded permissions, group-based scopes defined in a separate `groups.yaml`, Jira as a tier-1 adapter for triggers and audit mirroring, AWS as multi-account from day one, an open adapter registration mechanism so any subprocessor can be plugged in, and SCIM-aware termination. None of this is in v0.1, but the v0.1 spec includes the `identity.mode` and `state` placeholder fields and an open adapter registration mechanism so the v0.2 work is additive, not breaking. Read this document before you assume `procuracy hire` will work in your environment — and please file issues if any part of the v0.2 design doesn't fit your real-world flow.
