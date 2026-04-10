package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/procuracy/procuracy/internal/audit"
)

func TestRunHelp(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"--help"}, &out, &errBuf); code != 0 {
		t.Fatalf("--help exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "procuracy <command>") {
		t.Errorf("help output missing usage banner: %q", out.String())
	}
}

func TestRunVersion(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"version"}, &out, &errBuf); code != 0 {
		t.Fatalf("version exit = %d, want 0", code)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Error("version printed nothing")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"florp"}, &out, &errBuf); code != 2 {
		t.Fatalf("unknown cmd exit = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), `unknown command "florp"`) {
		t.Errorf("missing unknown-command message: %q", errBuf.String())
	}
}

func TestRunStubsExitNonZero(t *testing.T) {
	for _, cmd := range []string{"hire", "start", "fire", "auth"} {
		var out, errBuf bytes.Buffer
		if code := run([]string{cmd}, &out, &errBuf); code != 64 {
			t.Errorf("%s exit = %d, want 64", cmd, code)
		}
		if !strings.Contains(errBuf.String(), "not implemented") {
			t.Errorf("%s missing 'not implemented': %q", cmd, errBuf.String())
		}
	}
}

func TestDemoCreatesFilesAndExitsZero(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"demo"}, &out, &errBuf); code != 0 {
		t.Fatalf("demo exit = %d, stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "procuracy demo") {
		t.Errorf("missing banner in stdout: %q", out.String())
	}
	if !strings.Contains(out.String(), "procuracy validate") {
		t.Errorf("missing validate instruction: %q", out.String())
	}
	if !strings.Contains(out.String(), "procuracy verify") {
		t.Errorf("missing verify instruction: %q", out.String())
	}
	if !strings.Contains(out.String(), "tamper detection") {
		t.Errorf("missing tamper demo instruction: %q", out.String())
	}
}

func TestDemoFilesPassValidateAndVerify(t *testing.T) {
	var out, errBuf bytes.Buffer
	run([]string{"demo"}, &out, &errBuf)

	// Extract the manifest and audit log paths from the output.
	// Lines like "  /path/to/procuracy.yaml" and "  /path/to/audit.jsonl"
	var manifestPath, logPath string
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "procuracy.yaml") && !strings.Contains(line, "procuracy validate") && !strings.Contains(line, "cat ") {
			manifestPath = line
		}
		if strings.HasSuffix(line, "audit.jsonl") && !strings.Contains(line, "procuracy verify") && !strings.Contains(line, "sed ") && !strings.Contains(line, "cat ") {
			logPath = line
		}
	}
	if manifestPath == "" || logPath == "" {
		t.Fatalf("could not extract paths from demo output: manifest=%q log=%q", manifestPath, logPath)
	}

	// Validate the manifest.
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"validate", manifestPath}, &out, &errBuf); code != 0 {
		t.Fatalf("validate demo manifest exit = %d, stderr=%s", code, errBuf.String())
	}

	// Verify the audit log.
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"verify", logPath}, &out, &errBuf); code != 0 {
		t.Fatalf("verify demo audit log exit = %d, stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "6 entries verified") {
		t.Errorf("expected 6 entries (1 anchor + 5 sim), got: %q", out.String())
	}
}

func TestInitHappyPath(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	stdin := strings.NewReader("scout\nacme/docs\nread, write\n50\n5\ngithub.pull_request.merged\nprompts/default.md\n")
	var out, errBuf bytes.Buffer
	if code := cmdInit(nil, stdin, &out, &errBuf); code != 0 {
		t.Fatalf("init exit = %d, stderr=%s stdout=%s", code, errBuf.String(), out.String())
	}
	if !strings.Contains(out.String(), "Validation passed") {
		t.Errorf("expected validation passed message, got: %q", out.String())
	}
	// Check files exist.
	if _, err := os.Stat(filepath.Join(dir, "scout", "procuracy.yaml")); err != nil {
		t.Errorf("manifest not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scout", "prompts", "default.md")); err != nil {
		t.Errorf("prompt not created: %v", err)
	}
}

func TestInitMissingNameExitsNonZero(t *testing.T) {
	stdin := strings.NewReader("\n") // empty name
	var out, errBuf bytes.Buffer
	if code := cmdInit(nil, stdin, &out, &errBuf); code != 1 {
		t.Fatalf("init with empty name exit = %d, want 1", code)
	}
}

func TestValidateHappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "procuracy.yaml")
	if err := os.WriteFile(path, []byte(`
name: aria
identity:
  github_username: aria-acme
scopes:
  github:
    - read:org/*
triggers:
  - on: github.pull_request.merged
    do: review
runtime:
  engine: claude-code
  workspace: /tmp/aria
  cost_limit_daily_usd: 50
  cost_limit_per_task_usd: 5
handlers:
  review:
    type: claude_code
    prompt: prompts/review.md
`), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	if code := run([]string{"validate", path}, &out, &errBuf); code != 0 {
		t.Fatalf("validate exit = %d, stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "ok: aria") {
		t.Errorf("unexpected stdout: %q", out.String())
	}
}

func TestValidateBadFile(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"validate", "/nonexistent/path.yaml"}, &out, &errBuf); code != 1 {
		t.Fatalf("validate bad file exit = %d, want 1", code)
	}
}

func TestVerifyHappyPath(t *testing.T) {
	// Build a real audit log via the audit package, then verify it
	// through the CLI surface.
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := audit.Open(path, "aria")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		err := w.Append(audit.Entry{
			Type:        audit.TypeToolCall,
			Integration: "github",
			Verb:        "read",
			Resource:    "org/acme/repo",
			Result:      audit.ResultOK,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	var out, errBuf bytes.Buffer
	if code := run([]string{"verify", path}, &out, &errBuf); code != 0 {
		t.Fatalf("verify exit = %d, stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "ok: 4 entries verified") { // 1 anchor + 3 tool_calls
		t.Errorf("unexpected stdout: %q", out.String())
	}
}

func TestVerifyTamperedLogExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, _ := audit.Open(path, "aria")
	w.Append(audit.Entry{Type: audit.TypeToolCall, Verb: "read", Resource: "x", Result: audit.ResultOK})
	w.Close()

	// Corrupt one byte in the appended entry.
	raw, _ := os.ReadFile(path)
	corrupted := strings.Replace(string(raw), `"x"`, `"y"`, 1)
	if err := os.WriteFile(path, []byte(corrupted), 0644); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if code := run([]string{"verify", path}, &out, &errBuf); code != 1 {
		t.Fatalf("verify on tampered log exit = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "verify:") {
		t.Errorf("expected verify error in stderr, got: %q", errBuf.String())
	}
}

func TestVerifyMissingFile(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"verify", "/nonexistent/audit.jsonl"}, &out, &errBuf); code != 1 {
		t.Fatalf("verify missing file exit = %d, want 1", code)
	}
}

func TestVerifyNoArgsExits2(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"verify"}, &out, &errBuf); code != 2 {
		t.Fatalf("verify with no args exit = %d, want 2", code)
	}
}

func TestValidateEmitsV02Warnings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "procuracy.yaml")
	if err := os.WriteFile(path, []byte(`
name: aria
identity:
  mode: idp-managed
  github_username: aria-acme
scopes:
  github:
    - read:org/*
triggers:
  - on: github.pull_request.merged
    do: review
runtime:
  engine: claude-code
  workspace: /tmp/aria
  cost_limit_daily_usd: 50
  cost_limit_per_task_usd: 5
handlers:
  review:
    type: claude_code
    prompt: prompts/review.md
state:
  phase: requested
  requested_by: alice@company.com
`), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	if code := run([]string{"validate", path}, &out, &errBuf); code != 0 {
		t.Fatalf("validate exit = %d, stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "ok: aria") {
		t.Errorf("missing ok line: %q", out.String())
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("expected warnings in stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, "idp-managed") {
		t.Errorf("expected idp-managed warning, got: %q", stderr)
	}
	if !strings.Contains(stderr, "state block") {
		t.Errorf("expected state block warning, got: %q", stderr)
	}
}
