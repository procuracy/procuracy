<!--
Thanks for sending a PR. A few notes that will save us both time:

- procuracy is intentionally small. PRs that grow the surface area need a "why now" — link an issue or explain in the description.
- Adapters, templates, and engines are the easiest contributions. Spec changes and CLI changes need design discussion first.
- For non-trivial work, please open an issue before writing the patch so we can avoid duplication.
- All commits must pass `go vet`, `gofmt`, and `go test -race ./...`. CI will check.
-->

## What this changes

<!-- One paragraph. -->

## Why

<!-- The motivation. Link the issue if there is one. -->

## How

<!-- Brief sketch of the approach. Skip if obvious from the diff. -->

## Checklist

- [ ] `go vet ./...` clean
- [ ] `gofmt -l .` empty
- [ ] `go test -race ./...` passes
- [ ] If this changes the manifest schema, `docs/manifest-spec.md` is updated to match
- [ ] If this adds a new public CLI surface, the README and `cmd/procuracy/main.go` usage string are updated
- [ ] If this touches the capability/security model, I have explained the threat-model implications in the PR body
