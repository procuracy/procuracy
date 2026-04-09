package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, cmd := range []string{"hire", "start", "fire", "auth", "init"} {
		var out, errBuf bytes.Buffer
		if code := run([]string{cmd}, &out, &errBuf); code != 64 {
			t.Errorf("%s exit = %d, want 64", cmd, code)
		}
		if !strings.Contains(errBuf.String(), "not implemented") {
			t.Errorf("%s missing 'not implemented': %q", cmd, errBuf.String())
		}
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
