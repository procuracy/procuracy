// Package audit writes and verifies procuracy's hash-chained JSONL
// audit log.
//
// The on-disk format is documented in docs/audit-log.md. This package
// is the authoritative Go implementation; if the two ever disagree,
// the doc is the spec and this file is the bug.
//
// The trust property of the log is that any modification to a past
// entry — even a single byte — breaks the hash of every entry that
// follows. Verifying the chain is a single linear pass with one
// sha256 per entry. The Writer re-verifies on Open by default, so
// tampering surfaces immediately when procuracy reopens an existing
// log file rather than waiting for a manual verification later.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// rootPrevHash is the prev_hash value of the very first entry in any
// new log file. 64 hex zeros.
const rootPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

// procuracyVersion is stamped onto audit_anchor entries. Kept in sync
// with cmd/procuracy/main.go's version constant by hand for v0.1; a
// shared package will be introduced when there are three or more
// places that need it.
const procuracyVersion = "0.1.0-dev"

// Type is the closed enum of audit entry types. See docs/audit-log.md
// "Entry types" section for the full table.
type Type string

const (
	TypeAuditAnchor Type = "audit_anchor"
	TypeLifecycle   Type = "lifecycle"
	TypeToolCall    Type = "tool_call"
	TypeCost        Type = "cost"
	TypeCostBlocked Type = "cost_blocked"
	TypeError       Type = "error"
)

// Result is the closed enum for the result field of an action entry.
type Result string

const (
	ResultOK      Result = "ok"
	ResultBlocked Result = "blocked"
	ResultError   Result = "error"
)

// Entry is one line in the audit log. Field order in this struct is
// the field order in the JSON output (Go's encoding/json marshals
// struct fields in declaration order, which is what makes the hash
// input deterministic). DO NOT REORDER without bumping the spec.
//
// The Hash field is tagged omitempty so it drops out of the JSON
// encoding when set to "". This is what allows hashing the entry
// without the hash field present, then re-marshaling with the hash
// field set, in two passes against the same struct.
type Entry struct {
	Sequence         uint64    `json:"seq"`
	Timestamp        time.Time `json:"ts"`
	PrevHash         string    `json:"prev_hash"`
	Hash             string    `json:"hash,omitempty"`
	Contractor       string    `json:"contractor"`
	ProcuracyVersion string    `json:"procuracy_version,omitempty"`

	Type    Type   `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	Integration string  `json:"integration,omitempty"`
	Verb        string  `json:"verb,omitempty"`
	Resource    string  `json:"resource,omitempty"`
	Result      Result  `json:"result,omitempty"`
	CostUSD     float64 `json:"cost_usd,omitempty"`
	Error       string  `json:"error,omitempty"`

	Details map[string]any `json:"details,omitempty"`
}

// computeHash returns the hex sha256 of (prevHash bytes || canonical
// JSON of e with the Hash field cleared). The caller must NOT have
// already set e.Hash; the function asserts this with a clear-and-restore
// to be defensive against accidental misuse.
func (e *Entry) computeHash() (string, error) {
	saved := e.Hash
	e.Hash = ""
	body, err := json.Marshal(e)
	e.Hash = saved
	if err != nil {
		return "", fmt.Errorf("audit: marshal for hash: %w", err)
	}
	h := sha256.New()
	h.Write([]byte(e.PrevHash))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Writer appends entries to a procuracy audit log file. It is safe
// for concurrent use; Append is serialized through a mutex.
//
// A Writer is created with Open. Open verifies any existing chain
// before returning, so a successfully-opened Writer is guaranteed
// to be appending to a clean tail. The contract is: if Open returns
// nil, the audit log is in a known-good state.
type Writer struct {
	mu         sync.Mutex
	f          *os.File
	contractor string
	seq        uint64 // last written sequence number
	last       string // last written hash
}

// Open opens or creates an audit log at path for the named contractor.
//
// If the file exists, Open re-verifies the entire chain. Tampering
// detected at open is a hard error and Open returns it; the caller
// MUST NOT continue writing to a tampered log. If the file is new
// (or zero bytes), Open writes an audit_anchor:open entry as the
// chain root.
//
// Multiple processes opening the same path is unsupported and will
// produce a broken chain — see docs/audit-log.md §Concurrency.
func Open(path, contractor string) (*Writer, error) {
	if contractor == "" {
		return nil, fmt.Errorf("audit: contractor name is required")
	}
	// Stat first so we can decide between "new file, write anchor" and
	// "existing file, verify and continue."
	info, err := os.Stat(path)
	exists := err == nil && info.Size() > 0
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("audit: stat %s: %w", path, err)
	}

	w := &Writer{
		contractor: contractor,
		last:       rootPrevHash,
	}

	if exists {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("audit: open for verify: %w", err)
		}
		count, lastHash, err := verifyAndCount(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("audit: verify on open: %w", err)
		}
		w.seq = uint64(count)
		w.last = lastHash
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("audit: open for append: %w", err)
	}
	w.f = f

	if !exists {
		// New file: write the open anchor as the chain root.
		if err := w.appendLocked(Entry{
			Type:             TypeAuditAnchor,
			Subtype:          "open",
			ProcuracyVersion: procuracyVersion,
		}); err != nil {
			w.f.Close()
			return nil, fmt.Errorf("audit: write open anchor: %w", err)
		}
	}
	return w, nil
}

// Append writes a new entry to the audit log. The Sequence, Timestamp,
// PrevHash, Hash, and Contractor fields on e are set by the writer
// and any caller-provided values for these are overwritten. The
// caller is responsible for setting Type, Subtype, and the type-
// specific payload fields.
func (w *Writer) Append(e Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.appendLocked(e)
}

// appendLocked is the no-mutex variant. The caller must hold w.mu.
func (w *Writer) appendLocked(e Entry) error {
	w.seq++
	e.Sequence = w.seq
	e.Timestamp = time.Now().UTC()
	e.PrevHash = w.last
	e.Contractor = w.contractor
	if e.Type == "" {
		return fmt.Errorf("audit: entry has no type")
	}
	hash, err := e.computeHash()
	if err != nil {
		return err
	}
	e.Hash = hash
	body, err := json.Marshal(&e)
	if err != nil {
		return fmt.Errorf("audit: marshal entry: %w", err)
	}
	if _, err := w.f.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("audit: write entry: %w", err)
	}
	w.last = hash
	return nil
}

// Sync flushes any buffered data to disk. Useful for critical entries
// (cost_blocked, fired) where durability across an OS crash matters.
// Not called automatically — see docs/audit-log.md §Durability.
func (w *Writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	return w.f.Sync()
}

// Close closes the underlying file. Once Close returns, the Writer
// is no longer usable.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Sequence returns the most recently written sequence number. Useful
// for assertions in tests and for the runtime to know how many entries
// it has produced.
func (w *Writer) Sequence() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seq
}

// LastHash returns the most recently written entry's hash. Useful for
// rotation tooling that wants to record the final hash of a closed
// log file before opening a new one.
func (w *Writer) LastHash() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

// verifyAndCount is the engine behind both Verify (in verify.go) and
// the reopen-on-Open path. It reads every line, decodes each as an
// Entry, recomputes the hash chain, and returns (entryCount, lastHash,
// error). The error message includes the line number on which the
// chain broke.
func verifyAndCount(r io.Reader) (int, string, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var (
		count    int
		expected = rootPrevHash
		lastSeq  uint64
	)
	for dec.More() {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			return count, "", fmt.Errorf("line %d: decode: %w", count+1, err)
		}
		count++

		if e.Sequence != lastSeq+1 {
			return count, "", fmt.Errorf("line %d: sequence gap: got seq=%d, want seq=%d", count, e.Sequence, lastSeq+1)
		}
		if e.PrevHash != expected {
			return count, "", fmt.Errorf("line %d: chain broken: prev_hash=%s, expected %s", count, e.PrevHash, expected)
		}
		stored := e.Hash
		recomputed, err := e.computeHash()
		if err != nil {
			return count, "", fmt.Errorf("line %d: %w", count, err)
		}
		if recomputed != stored {
			return count, "", fmt.Errorf("line %d: hash mismatch: stored=%s, recomputed=%s", count, stored, recomputed)
		}
		if e.Contractor == "" {
			return count, "", fmt.Errorf("line %d: missing contractor", count)
		}
		if e.Type == "" {
			return count, "", fmt.Errorf("line %d: missing type", count)
		}

		lastSeq = e.Sequence
		expected = stored
	}
	return count, expected, nil
}
