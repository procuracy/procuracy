package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ----- helpers -----

// readLines returns each line of a file as a separate string.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		return nil
	}
	out := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	return out
}

// writeLines replaces the file contents with the given lines, joining
// them with "\n" and adding a trailing newline.
func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// ----- happy path -----

func TestOpenWritesAnchor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (the open anchor)", len(lines))
	}
	var e Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Type != TypeAuditAnchor || e.Subtype != "open" {
		t.Errorf("got type=%q subtype=%q, want audit_anchor/open", e.Type, e.Subtype)
	}
	if e.Sequence != 1 {
		t.Errorf("seq = %d, want 1", e.Sequence)
	}
	if e.PrevHash != rootPrevHash {
		t.Errorf("prev_hash = %q, want all zeros", e.PrevHash)
	}
	if e.Contractor != "aria" {
		t.Errorf("contractor = %q", e.Contractor)
	}
	if e.ProcuracyVersion == "" {
		t.Error("anchor missing procuracy_version")
	}
}

func TestRoundTripVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		err := w.Append(Entry{
			Type:        TypeToolCall,
			Integration: "github",
			Verb:        "read",
			Resource:    fmt.Sprintf("org/acme/repo/PR-%d", i),
			Result:      ResultOK,
			Details:     map[string]any{"pr_number": i},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	count, err := VerifyFile(path)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 6 { // 1 anchor + 5 tool_calls
		t.Errorf("count = %d, want 6", count)
	}
}

func TestAppendOverwritesChainFields(t *testing.T) {
	// Caller-provided sequence/prev_hash/timestamp/hash should be ignored.
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	err = w.Append(Entry{
		Sequence: 9999,
		PrevHash: "deadbeef",
		Hash:     "deadbeef",
		Type:     TypeToolCall,
		Verb:     "read",
		Resource: "x",
		Result:   ResultOK,
	})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	count, err := VerifyFile(path)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestEntryWithoutTypeIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	err = w.Append(Entry{Verb: "read"})
	if err == nil {
		t.Fatal("expected error for entry without type")
	}
	if !strings.Contains(err.Error(), "no type") {
		t.Errorf("error = %v, want 'no type'", err)
	}
}

func TestEmptyContractorRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	_, err := Open(path, "")
	if err == nil {
		t.Fatal("expected error for empty contractor")
	}
}

// ----- tamper detection -----

func TestTamperPayloadByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		w.Append(Entry{
			Type:     TypeToolCall,
			Verb:     "read",
			Resource: fmt.Sprintf("res-%d", i),
			Result:   ResultOK,
		})
	}
	w.Close()

	// Modify the resource on the second tool_call entry (line index 2:
	// index 0 is the anchor, index 1 is res-0, index 2 is res-1).
	lines := readLines(t, path)
	lines[2] = strings.Replace(lines[2], "res-1", "res-X", 1)
	writeLines(t, path, lines)

	_, err = VerifyFile(path)
	if err == nil {
		t.Fatal("verify should have failed on tampered payload")
	}
	// We expect the failure on line 3 (1-indexed): line 1 is anchor (ok),
	// line 2 is res-0 (ok), line 3 is the tampered res-1 entry whose
	// recomputed hash will mismatch the stored hash.
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error should reference line 3, got: %v", err)
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("error should mention hash mismatch, got: %v", err)
	}
}

func TestTamperHashField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	w.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: "x", Result: ResultOK})
	w.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: "y", Result: ResultOK})
	w.Close()

	// Replace the hash field on line 2 (the first tool_call) with garbage.
	lines := readLines(t, path)
	var e Entry
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil {
		t.Fatal(err)
	}
	e.Hash = "0000000000000000000000000000000000000000000000000000000000000000"
	body, _ := json.Marshal(&e)
	lines[1] = string(body)
	writeLines(t, path, lines)

	_, err = VerifyFile(path)
	if err == nil {
		t.Fatal("verify should have failed on tampered hash field")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should reference line 2, got: %v", err)
	}
}

func TestTamperPrevHashField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	w.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: "x", Result: ResultOK})
	w.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: "y", Result: ResultOK})
	w.Close()

	// Replace the prev_hash on line 3 with all zeros (re-rooting it).
	lines := readLines(t, path)
	var e Entry
	if err := json.Unmarshal([]byte(lines[2]), &e); err != nil {
		t.Fatal(err)
	}
	e.PrevHash = rootPrevHash
	body, _ := json.Marshal(&e)
	lines[2] = string(body)
	writeLines(t, path, lines)

	_, err = VerifyFile(path)
	if err == nil {
		t.Fatal("verify should have failed on tampered prev_hash")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error should reference line 3, got: %v", err)
	}
	if !strings.Contains(err.Error(), "chain broken") {
		t.Errorf("error should mention chain broken, got: %v", err)
	}
}

// ----- structural failures -----

func TestSequenceGapDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	w.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: "x", Result: ResultOK})
	w.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: "y", Result: ResultOK})
	w.Close()

	// Delete the middle line.
	lines := readLines(t, path)
	lines = append(lines[:1], lines[2:]...)
	writeLines(t, path, lines)

	_, err = VerifyFile(path)
	if err == nil {
		t.Fatal("verify should have failed on sequence gap")
	}
	// After deleting line 2, the new line 2 has seq=3 but expected seq=2.
	if !strings.Contains(err.Error(), "sequence gap") {
		t.Errorf("error should mention sequence gap, got: %v", err)
	}
}

func TestBadJSONLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, _ := Open(path, "aria")
	w.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: "x", Result: ResultOK})
	w.Close()

	lines := readLines(t, path)
	lines[1] = "{this is not valid json"
	writeLines(t, path, lines)

	_, err := VerifyFile(path)
	if err == nil {
		t.Fatal("verify should have failed on bad JSON")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should reference line 2, got: %v", err)
	}
}

func TestEmptyFileRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyFile(path)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	// A line with an unknown field is rejected by the strict decoder.
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, _ := Open(path, "aria")
	w.Close()

	// Inject a line with an extra field.
	body := `{"seq":2,"ts":"2026-04-09T19:30:00Z","prev_hash":"a","hash":"b","contractor":"aria","type":"tool_call","extra_field":"nope"}`
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(body + "\n")
	f.Close()

	_, err := VerifyFile(path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

// ----- reopen and continue -----

func TestReopenContinuesChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	// First session: write 3 entries.
	w1, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		w1.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: fmt.Sprintf("session1-%d", i), Result: ResultOK})
	}
	lastSeq1 := w1.Sequence()
	lastHash1 := w1.LastHash()
	w1.Close()

	// Second session: reopen, write 2 more entries.
	w2, err := Open(path, "aria")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if w2.Sequence() != lastSeq1 {
		t.Errorf("Sequence after reopen = %d, want %d", w2.Sequence(), lastSeq1)
	}
	if w2.LastHash() != lastHash1 {
		t.Errorf("LastHash after reopen = %s, want %s", w2.LastHash(), lastHash1)
	}
	for i := 0; i < 2; i++ {
		w2.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: fmt.Sprintf("session2-%d", i), Result: ResultOK})
	}
	w2.Close()

	// Verify the whole thing: 1 anchor + 3 + 2 = 6 entries.
	count, err := VerifyFile(path)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 6 {
		t.Errorf("count = %d, want 6", count)
	}
}

func TestReopenRejectedOnTamper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, _ := Open(path, "aria")
	w.Append(Entry{Type: TypeToolCall, Verb: "read", Resource: "x", Result: ResultOK})
	w.Close()

	// Corrupt the file.
	lines := readLines(t, path)
	lines[1] = strings.Replace(lines[1], `"x"`, `"y"`, 1)
	writeLines(t, path, lines)

	_, err := Open(path, "aria")
	if err == nil {
		t.Fatal("Open should have rejected tampered log")
	}
	if !strings.Contains(err.Error(), "verify on open") {
		t.Errorf("error = %v, want 'verify on open'", err)
	}
}

// ----- concurrency -----

func TestConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 10
	const perGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				err := w.Append(Entry{
					Type:     TypeToolCall,
					Verb:     "read",
					Resource: fmt.Sprintf("g%d-%d", id, i),
					Result:   ResultOK,
				})
				if err != nil {
					t.Errorf("append: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	w.Close()

	count, err := VerifyFile(path)
	if err != nil {
		t.Fatalf("verify after concurrent appends: %v", err)
	}
	want := 1 + goroutines*perGoroutine
	if count != want {
		t.Errorf("count = %d, want %d", count, want)
	}
}

// ----- determinism / regression guard -----

func TestEntryHashDeterminism(t *testing.T) {
	// Two identical entries (same prev_hash, same content) must produce
	// the same hash. This guards against accidental nondeterminism in
	// the canonicalizer.
	mk := func() Entry {
		return Entry{
			Sequence:    42,
			PrevHash:    "abc",
			Contractor:  "aria",
			Type:        TypeToolCall,
			Integration: "github",
			Verb:        "read",
			Resource:    "org/acme/repo",
			Result:      ResultOK,
			Details: map[string]any{
				"z_last":  "z",
				"a_first": "a",
				"m_mid":   "m",
			},
		}
	}
	e1 := mk()
	e2 := mk()
	h1, err := e1.computeHash()
	if err != nil {
		t.Fatal(err)
	}
	h2, err := e2.computeHash()
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("identical entries hashed differently: %s vs %s", h1, h2)
	}
}

func TestVerifyEmptyReader(t *testing.T) {
	// An empty io.Reader (not a file) should produce 0 entries with no
	// error. The "empty file is an error" rule is enforced only by
	// VerifyFile, not by Verify.
	count, err := Verify(bytes.NewReader(nil))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}
