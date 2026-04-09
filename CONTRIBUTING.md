# Contributing to procuracy

Thanks for your interest. procuracy is **alpha** and intentionally small — the goal is to ship a tight, opinionated framework rather than a sprawling one. That means contributions are welcome in some areas more than others.

---

## What we need most

In rough order of impact:

1. **Adapters** for new integrations — Jira, Notion, GitLab, Bitbucket, Discord, Asana, Trello. Each adapter is a self-contained Go package that exposes a typed capability set; the capability layer enforces scopes at construction time. See `internal/adapters/` (once it exists) and the GitHub adapter as the reference.
2. **Templates** for new contractor roles — single-purpose, opinionated, one-command-clonable. Templates are just directories with a `procuracy.yaml` and prompt files; **no Go code required**. The template marketplace is the adoption flywheel — every new template is a new use case the project supports out of the box.
3. **Engine adapters** for non-Claude runtimes — OpenHands, OpenAI Assistants, custom binary protocol. The engine interface is intentionally narrow.
4. **Security review** of the capability enforcement layer. This is the load-bearing differentiator; eyes on it from people who have shipped capability-based systems before are very welcome.
5. **Documentation** in any language. Translations of the README and `docs/manifest-spec.md` are first-class.

## What we don't need

Please don't open PRs for these without discussing first — we'll likely close them:

- **Refactors with no behavior change.** The codebase is small enough that "cleanup for cleanup's sake" mostly creates merge conflicts.
- **New top-level manifest fields** without a use case the existing fields can't express. The spec is intentionally tight; widening it later is much easier than narrowing it.
- **Web UI / dashboard.** procuracy is deliberately CLI-only — the Slack channel and the audit log are the dashboard. Adding a UI defeats the "no SaaS dependency" pitch.
- **Plugin systems / dynamic loading.** Adapters are statically linked. This is a feature.
- **Telemetry, even opt-in.** No phone-home is non-negotiable.

## Before you start

For anything beyond a typo or a one-line fix, **open an issue first** and link it from the PR. This avoids duplicate work and gives us a place to debate scope before you've sunk time into it.

Adapter and template PRs without a prior issue are usually fine — those are the highest-leverage contributions and we'd rather review them than gate them.

## Development

### Prerequisites

- Go 1.25 or later
- `git`
- `gh` (GitHub CLI) — only needed if you plan to use `gh repo create` style flows in templates

### Build

```bash
git clone https://github.com/procuracy/procuracy
cd procuracy
go build ./cmd/procuracy
./procuracy version
```

### Test

```bash
go test -race ./...
```

CI runs `go vet ./...`, `gofmt -l .` (must be empty), `go build ./...`, and `go test -race ./...` on Linux and macOS. Match it locally before pushing.

### Project layout

```
cmd/procuracy/      CLI dispatch shim
internal/manifest/  YAML parser + validator (the load-bearing artifact)
internal/...        adapters, capability layer, audit, runtime (most TBD)
docs/               manifest spec, security model, audit log spec
examples/           shipped templates
.github/            CI, issue/PR templates, dependabot
```

## Pull requests

- Branch off `main`. Keep the diff focused — one logical change per PR.
- Match existing style: `gofmt`, no exported identifiers without doc comments, error wrapping with `fmt.Errorf("...: %w", err)`.
- Update `docs/manifest-spec.md` if you change the manifest schema. **The spec doc is authoritative; if the parser and the doc disagree, the doc is the spec and the parser is the bug.**
- Add tests. Pure validation logic should be table-driven; adapters should have at least one round-trip test against a recorded API fixture.
- Write a real commit message — explain *why*, not just *what*. The diff already says what.

## Manifest spec changes

The `procuracy.yaml` schema is the load-bearing artifact of the whole project. Changes to it have a higher bar:

- Open an issue first, tagged `spec`. Describe the use case the existing schema can't express.
- Update `docs/manifest-spec.md` *and* `internal/manifest/manifest.go` *and* the test cases in the same PR.
- Mark whether the change is additive (safe) or breaking (needs major version bump).
- If breaking, propose a migration path for existing manifests.

## Security

If you find a security vulnerability — especially in the capability enforcement layer or the cost interceptor — **please do not file a public issue**. Open a private security advisory at https://github.com/procuracy/procuracy/security/advisories/new and we'll respond.

For non-vulnerability security questions (threat model, design choices), public discussion is fine.

## Code of conduct

Be kind and direct. Disagreement is welcome; personal attacks are not. We follow the [Contributor Covenant](https://www.contributor-covenant.org/version/2/1/code_of_conduct/) for the spirit, even if we don't formally codify it.

## License

By contributing to procuracy you agree your contributions will be licensed under the [Apache License 2.0](LICENSE).
