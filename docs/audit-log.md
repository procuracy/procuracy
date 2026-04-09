# Audit log format specification

**Status:** v0.1 (alpha — additive changes only; existing fields will not be removed without a major version bump)

The audit log is procuracy's trust layer. Every action a contractor takes — every API call, every file edit, every dollar of LLM spend, every state transition — is recorded as one append-only line in a hash-chained JSONL file. The chain makes tampering detectable: any modification to a past entry, even a single byte, breaks the hash of every entry that follows.

**The audit log is what makes adoption possible.** Without it, no security review will pass. With it, the question "what did this bot do last Tuesday?" is `grep` against a file, and "did anyone tamper with the log?" is `procuracy verify <path>`.

This document is the authoritative reference. The Go writer in [`internal/audit`](../internal/audit) is generated to match.

---

## On-disk format

A procuracy audit log is a UTF-8 text file containing one JSON object per line, separated by `\n`. The file ends with a trailing newline. There is no other framing — no length prefix, no length-encoded record header, no binary content. You can `cat` it, `tail` it, `grep` it, and `wc -l` it. This is intentional: the audit log is a forensic artifact that has to remain readable and processable with standard Unix tooling decades after it was written, even if procuracy itself has gone away.

Example (whitespace added for readability — real entries are single-line):

```json
{"seq":1,"ts":"2026-04-09T19:30:00.000000000Z","prev_hash":"0000000000000000000000000000000000000000000000000000000000000000","hash":"a1b2c3...","contractor":"aria","procuracy_version":"0.1.0-dev","type":"audit_anchor","subtype":"open"}
{"seq":2,"ts":"2026-04-09T19:30:15.123456789Z","prev_hash":"a1b2c3...","hash":"d4e5f6...","contractor":"aria","type":"tool_call","integration":"github","verb":"read","resource":"org/acme/repo/PR-42","result":"ok","details":{"pr_number":42,"author":"alice"}}
{"seq":3,"ts":"2026-04-09T19:30:16.456789012Z","prev_hash":"d4e5f6...","hash":"7g8h9i...","contractor":"aria","type":"cost","cost_usd":0.0234,"details":{"model":"claude-opus-4-6","input_tokens":1500,"output_tokens":300}}
```

## Entry shape

Every entry is a JSON object with this canonical field order. Fields with no value are omitted (not written as `null`). The field order in JSON output is the order below — this is enforced by the Go writer using a typed struct with fields in declaration order, which Go's `encoding/json` serializes deterministically.

| Field | Type | Required | Description |
|---|---|:-:|---|
| `seq` | uint64 | ✓ | Monotonic sequence number, starting at 1. The first entry in any new log file has `seq:1` (the open anchor). Sequence numbers are dense — no gaps allowed. |
| `ts` | RFC3339Nano string | ✓ | Wall-clock timestamp in UTC with nanosecond precision. Used for human ordering and forensic timelining. **Not used as a tamper detection mechanism** — use the hash chain for that. |
| `prev_hash` | hex string (64 chars) | ✓ | The hash of the previous entry. The first entry in any new log file has `prev_hash` set to 64 zeros. |
| `hash` | hex string (64 chars) | ✓ | This entry's hash. See *Hash chain algorithm* below. |
| `contractor` | string | ✓ | The name of the contractor (matches `name:` in `procuracy.yaml`). Lets a single audit log file be auditable on its own without external context. |
| `procuracy_version` | string | optional | The procuracy version that wrote the entry. Always set on `audit_anchor` entries; optional on others. Useful when a log spans multiple procuracy upgrades. |
| `type` | enum | ✓ | The entry type. See *Entry types* below. |
| `subtype` | string | optional | A type-specific subdivision. For `audit_anchor` it is `open` or `close`. |
| `integration` | string | optional | The adapter that the action targeted (e.g. `github`, `slack`). Set on `tool_call` and `error` entries that originated from an adapter. |
| `verb` | string | optional | The capability verb that was exercised (e.g. `read`, `write`, `merge`). Set on `tool_call`. |
| `resource` | string | optional | The resource the verb was applied to (e.g. `org/acme/docs/api.md`). Set on `tool_call`. |
| `result` | enum | optional | One of `ok`, `blocked`, `error`. Set on `tool_call` and `cost` entries. |
| `cost_usd` | number | optional | USD cost of the action, with whatever precision the source provides (typically 4 decimal places for LLM calls). Set on `cost` and `cost_blocked` entries. |
| `error` | string | optional | Human-readable error message. Set on `error` entries and on `tool_call` / `cost` entries with `result: error`. |
| `details` | object | optional | Free-form structured data. Used for everything that doesn't fit the typed fields above — request payloads, response IDs, decision rationale, etc. Keys are alphabetically sorted in the JSON output (Go `encoding/json` enforces this for `map[string]any`), so the encoding is reproducible. |

## Entry types

The `type` field is a closed enum. Adding a new type is an additive change; renaming or removing a type is a major version bump.

| Type | Purpose | Required fields beyond the chain |
|---|---|---|
| `audit_anchor` | System entry. Written automatically by the writer at file open (`subtype: open`) and at graceful close (`subtype: close`, when graceful shutdown lands in v0.2). Anchors the chain to wall-clock time and procuracy version. | `subtype`, `procuracy_version` |
| `lifecycle` | Contractor lifecycle transition: hired, started, paused, updated, fired. Written by the CLI when those commands run. Not yet implemented in v0.1 — the commands themselves are stubs. | `subtype` (one of: `hired`, `started`, `paused`, `updated`, `fired`) |
| `tool_call` | An adapter method was invoked. The most common entry type once real adapters land. | `integration`, `verb`, `resource`, `result` |
| `cost` | An LLM API call's cost was recorded. Written by the cost interceptor on every successful call. | `cost_usd`, `details` (model, tokens) |
| `cost_blocked` | An LLM API call was blocked because it would exceed the contractor's daily or per-task budget. The call did **not** happen. This is the structurally enforced "fail closed on cost" property from the README's security model. | `cost_usd` (the requested amount), `details` (limit, current spend) |
| `error` | Anything that went wrong outside of a normal `tool_call` flow — adapter construction failures, manifest reload errors, IdP cascade timeouts, etc. | `error` |

**v0.1 status:** none of the entry types other than `audit_anchor` are written yet, because the runtime that would write them does not exist yet. The types are defined now so that adapters, the cost interceptor, and the lifecycle commands all have a target shape to write into when they land. Adding new types in v0.2+ is non-breaking.

## Hash chain algorithm

Each entry's `hash` is computed as:

```
hash = sha256( prev_hash_bytes || canonical_json_without_hash_field )
```

In Go terms:

```go
// Compute hash of an entry that does not yet have its hash field set.
inputBytes := append([]byte(prevHash), canonicalJSONOf(entry)...)
sum := sha256.Sum256(inputBytes)
entry.Hash = hex.EncodeToString(sum[:])
```

Where `canonicalJSONOf(entry)` is the entry serialized via `json.Marshal` with the `Hash` field set to the empty string and tagged `omitempty`, so the field drops out of the encoding entirely. This makes the hash input the entry-without-its-hash-field, which is the only way the chain can be verified after the fact: a verifier recomputes the same hash and compares.

The first entry in any new log file has:

```
prev_hash = "0000000000000000000000000000000000000000000000000000000000000000"   (64 zeros)
```

This is the chain root. A verifier knows it has reached the start of the file when it sees a `prev_hash` of all zeros.

### Why sha256

- Universally available, no platform dependencies
- Fast enough that hashing every audit entry costs <1µs per entry
- 256 bits is overkill for tamper detection but the alternative (sha1) is deprecated and the alternative-alternative (blake2b) adds a dependency for no real benefit
- Same algorithm Sigstore, Git, and the Bitcoin chain use — operationally familiar to security teams

There is no algorithm field in the entry shape. v0.2 may add one (additive) if a future audit needs a different hash, but v0.1 is sha256-only.

### Why include `prev_hash` in the entry instead of computing it from neighbors

A verifier could in principle compute each entry's `prev_hash` by taking the hash of the previous line. But storing it inline has two real benefits:

1. **A single line is self-describing.** You can grep one entry out of a 10 GB log and it carries enough information to chain-verify against the previous entry without rereading the file.
2. **Tampering with `prev_hash` is itself detectable.** Because `prev_hash` is part of the canonical JSON that `hash` is computed over, modifying it breaks `hash`. A verifier catches both kinds of tampering with one check.

## Verification semantics

A verifier reads the file line by line and maintains a running expected previous hash, starting at `"0" * 64`. For each line:

1. Decode the JSON into an `Entry`.
2. Compare the entry's `prev_hash` to the running expected previous hash. If they differ, the chain is broken — return error with the line number and both hash values.
3. Compute the entry's hash exactly as the writer did: serialize the entry with `Hash` set empty (omitted from output via `omitempty`), prepend the `prev_hash` bytes, sha256 the result, hex-encode.
4. Compare the computed hash to the entry's stored `hash`. If they differ, the entry has been modified — return error with the line number and both hashes.
5. Verify that `seq` is exactly one greater than the previous entry's `seq` (or `1` if this is the first entry). Sequence gaps are an error — they indicate either a missing entry or a forged sequence number that the rest of the chain still validates against.
6. Update the running expected previous hash to this entry's `hash`.
7. Continue.

A successfully verified file ends at EOF with the running count returned and no error. The `procuracy verify <path>` command surfaces this directly:

```
$ procuracy verify ./contractors/aria/audit.jsonl
ok: 1247 entries verified
```

A failure looks like:

```
$ procuracy verify ./contractors/aria/audit.jsonl
verify: chain broken at line 89: hash mismatch (expected a1b2c3..., got d4e5f6...)
```

The verifier exit code is `0` for a clean log and `1` for any kind of failure.

## File location

By default, the audit log lives at:

```
<runtime.workspace>/audit.jsonl
```

This is overridable via the manifest's `observability.audit_log_path` field. If both are set, the explicit path wins. If neither is set, the writer falls back to `./audit.jsonl` relative to the working directory — useful for tests, never for production.

The directory must exist before the writer opens the file. The writer will not auto-create parent directories — that is the runtime's job, not the audit layer's, because failing to create a directory is a configuration error that should surface loudly at startup, not a recoverable condition the audit layer silently masks.

## Concurrency

A single procuracy process opens a single `Writer` per log file. The writer holds an `*os.File` in `O_APPEND | O_WRONLY | O_CREATE` mode and a mutex. Multiple goroutines may call `Append` concurrently; the mutex serializes them. There is no buffering between `Append` and `write(2)` — every entry hits the OS write buffer immediately. By default the OS flushes on its own schedule; `Sync()` flushes to disk explicitly (see *Durability*).

Multiple processes appending to the same log file is **not supported in v0.1**. The hash chain assumes a single writer's view of "what is the previous entry." Two processes appending in parallel would race and produce a broken chain. If you need multi-process writes, run them through a single procuracy process or write to separate files. v0.2 may add a flock-based coordination protocol if real demand emerges.

## Durability

`Append` returns after the entry has been written to the OS write buffer. By default this means the entry is durable across a process crash but **not** across an OS crash or power loss. For full durability, the runtime should call `Writer.Sync()` after critical entries (cost-blocked, fired, capability errors). v0.1 does not auto-sync; v0.2 will add a `sync_after:` policy field to the `observability` manifest block.

Trade-off: syncing on every entry would make every action ~10ms slower on a typical SSD. Not syncing at all loses a few seconds of audit history if the OS crashes. Selective syncing on critical entries is the v0.2 plan.

## Reopening an existing log

When `Open()` is called on a path that already exists, the writer:

1. Calls `Verify()` on the entire file. **Tampering detected at open is a hard error and the writer refuses to construct.** This is intentional: if the audit log has been tampered with, the right reaction is to alert the operator immediately, not to silently continue appending and let the tampering go unnoticed until the next manual verification.
2. Records the last entry's `seq` and `hash` as the running state.
3. Continues appending from there. The next `Append` produces an entry with `seq = lastSeq + 1` and `prev_hash = lastHash`.
4. Does **not** write a new `audit_anchor:open` entry on reopen. Anchors are only written when a brand new file is created.

This means opening a 1 GB log re-reads and re-verifies the entire file, which can take a few seconds. For v0.1 this is the right tradeoff: correctness over speed, and the operator gets a guaranteed-clean tail to append to. v0.2 may add an "unsafe-fast-reopen" mode that skips re-verification and trusts the operator.

## Rotation

procuracy v0.1 does **not** rotate audit logs automatically. The file grows unbounded.

Recommended manual rotation, while the contractor is paused (so no writes are in flight):

```bash
procuracy pause aria
mv ./contractors/aria/audit.jsonl ./contractors/aria/audit-$(date +%Y%m%d).jsonl
procuracy verify ./contractors/aria/audit-$(date +%Y%m%d).jsonl
procuracy start aria
```

The new audit log file starts fresh with a new `audit_anchor:open` and a new chain. Rotation files are independent — there is intentionally no cross-file chain link, because the operator should be able to delete or archive old rotations without invalidating current ones.

v0.2 adds a `rotate:` policy field to `observability` that lets procuracy do this automatically based on file size, age, or entry count.

## What is **not** in v0.1

- **Slack mirror.** v0.1 writes only the local JSONL. The Slack mirror lands with the Slack adapter in v0.2.
- **Log rotation.** Manual `mv` only.
- **Compression.** Logs are uncompressed UTF-8. Operators can compress rotated files with `gzip` after rotation; the `.jsonl.gz` is still tamper-evident as long as the rotation file's chain was clean at rotation time.
- **Streaming verification of growing logs.** `Verify()` reads the file once and stops at EOF. To verify a log that is currently being appended to, snapshot it first (`cp`).
- **Cross-file chain linking.** Each rotation file is its own independent chain. v0.2 may add an `audit_anchor:rotate` entry that records the previous rotation file's final hash for cross-file traceability.
- **External signing.** The chain is integrity-protected but not authenticity-protected. v0.2 may add an `audit_anchor:open` field carrying an HSM-signed root hash for cross-process / cross-host trust. For v0.1, the trust boundary is the operator's filesystem.
- **Encryption at rest.** Use filesystem-level encryption (LUKS, FileVault, BitLocker, EFS).
- **Multi-process writers.** Single process per log file in v0.1.

These are deliberate carve-outs, not oversights. Each is documented so operators know exactly where the v0.1 trust boundary is.
