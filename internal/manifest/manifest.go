// Package manifest parses and validates procuracy.yaml files.
//
// The schema is documented in docs/manifest-spec.md. This package is the
// authoritative Go representation; if the two ever disagree, the doc is
// the spec and this file is the bug.
package manifest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Manifest is the root of a procuracy.yaml file.
type Manifest struct {
	Name          string            `yaml:"name"`
	DisplayName   string            `yaml:"display_name,omitempty"`
	Description   string            `yaml:"description,omitempty"`
	Identity      Identity          `yaml:"identity"`
	Scopes        Scopes            `yaml:"scopes"`
	Triggers      []Trigger         `yaml:"triggers"`
	Runtime       Runtime           `yaml:"runtime"`
	Handlers      map[string]Handler `yaml:"handlers"`
	Observability *Observability    `yaml:"observability,omitempty"`
	Termination   *Termination      `yaml:"termination,omitempty"`
}

// Identity is the contractor's account presence on each integration.
type Identity struct {
	Email          string `yaml:"email,omitempty"`
	GitHubUsername string `yaml:"github_username,omitempty"`
	SlackHandle    string `yaml:"slack_handle,omitempty"`
	LinearUser     string `yaml:"linear_user,omitempty"`
}

// Scopes is the capability declaration. Keys are integration names; values
// are lists of scope strings (see docs/manifest-spec.md §scopes).
type Scopes map[string][]string

// Trigger describes when the contractor wakes up.
type Trigger struct {
	On    string `yaml:"on"`
	Where string `yaml:"where,omitempty"`
	Cron  string `yaml:"cron,omitempty"`
	Do    string `yaml:"do"`
}

// Runtime describes how the contractor thinks and what it costs.
type Runtime struct {
	Engine               string  `yaml:"engine"`
	Model                string  `yaml:"model,omitempty"`
	Workspace            string  `yaml:"workspace"`
	CostLimitDailyUSD    float64 `yaml:"cost_limit_daily_usd"`
	CostLimitPerTaskUSD  float64 `yaml:"cost_limit_per_task_usd"`
	TimeoutPerTaskSecs   int     `yaml:"timeout_per_task_seconds,omitempty"`
	MaxConcurrentTasks   int     `yaml:"max_concurrent_tasks,omitempty"`
}

// Handler is a named unit of work referenced by triggers.
type Handler struct {
	Type    string   `yaml:"type"`
	Prompt  string   `yaml:"prompt,omitempty"`
	Command []string `yaml:"command,omitempty"`
}

// Observability configures audit log mirrors. The local JSONL log is always
// written regardless of this block.
type Observability struct {
	AuditChannel string `yaml:"audit_channel,omitempty"`
	Metrics      string `yaml:"metrics,omitempty"`
	AuditLogPath string `yaml:"audit_log_path,omitempty"`
}

// Termination describes the steps `procuracy fire` runs to revoke a contractor.
type Termination struct {
	OnKillSignal []map[string]any `yaml:"on_kill_signal,omitempty"`
}

// reservedIntegrations is the v0.1 closed set of adapter names. Adding to
// this list is a non-breaking change; using a name not in the list is a
// validation error.
var reservedIntegrations = map[string]bool{
	"github":    true,
	"slack":     true,
	"linear":    true,
	"jira":      true,
	"notion":    true,
	"email":     true,
	"gitlab":    true,
	"bitbucket": true,
	"discord":   true,
}

var (
	nameRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	handlerRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Load reads a manifest file from disk, parses it, and validates it.
// The returned manifest is safe to use; any error means it is not.
func Load(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return Parse(raw, filepath.Dir(path))
}

// Parse decodes raw YAML and validates it. baseDir is used to resolve
// relative paths inside the manifest (handler prompts, audit log path);
// pass "" if you do not have one.
func Parse(raw []byte, baseDir string) (*Manifest, error) {
	var m Manifest

	// Strict decoding: unknown top-level keys are an error, not a warning.
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate runs the spec's validation pipeline (stages 2–4). Stage 5
// (adapter validation) lives in the adapter registry and is not run here.
func (m *Manifest) Validate() error {
	// Stage 2: required fields.
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if len(m.Triggers) == 0 {
		return fmt.Errorf("manifest: at least one trigger is required")
	}
	if len(m.Handlers) == 0 {
		return fmt.Errorf("manifest: at least one handler is required")
	}
	if m.Runtime.Engine == "" {
		return fmt.Errorf("manifest: runtime.engine is required")
	}
	if m.Runtime.Workspace == "" {
		return fmt.Errorf("manifest: runtime.workspace is required")
	}

	// Stage 3: field shape.
	if !nameRE.MatchString(m.Name) {
		return fmt.Errorf("manifest: name %q must match %s", m.Name, nameRE)
	}
	if !filepath.IsAbs(m.Runtime.Workspace) {
		return fmt.Errorf("manifest: runtime.workspace %q must be an absolute path", m.Runtime.Workspace)
	}
	if m.Runtime.CostLimitDailyUSD <= 0 {
		return fmt.Errorf("manifest: runtime.cost_limit_daily_usd must be > 0")
	}
	if m.Runtime.CostLimitPerTaskUSD <= 0 {
		return fmt.Errorf("manifest: runtime.cost_limit_per_task_usd must be > 0")
	}
	if m.Runtime.CostLimitPerTaskUSD > m.Runtime.CostLimitDailyUSD {
		return fmt.Errorf("manifest: runtime.cost_limit_per_task_usd (%.2f) must be <= cost_limit_daily_usd (%.2f)",
			m.Runtime.CostLimitPerTaskUSD, m.Runtime.CostLimitDailyUSD)
	}
	for i, t := range m.Triggers {
		if t.On == "" {
			return fmt.Errorf("manifest: triggers[%d].on is required", i)
		}
		if t.Do == "" {
			return fmt.Errorf("manifest: triggers[%d].do is required", i)
		}
		if t.On == "schedule" && t.Cron == "" {
			return fmt.Errorf("manifest: triggers[%d] has on=schedule but no cron", i)
		}
		if t.On != "schedule" && t.Cron != "" {
			return fmt.Errorf("manifest: triggers[%d] has cron but on=%q (cron only valid with on=schedule)", i, t.On)
		}
	}
	for name, h := range m.Handlers {
		if !handlerRE.MatchString(name) {
			return fmt.Errorf("manifest: handler name %q must match %s", name, handlerRE)
		}
		switch h.Type {
		case "claude_code":
			if h.Prompt == "" {
				return fmt.Errorf("manifest: handler %q (type=claude_code) requires a prompt", name)
			}
		case "script":
			if len(h.Command) == 0 {
				return fmt.Errorf("manifest: handler %q (type=script) requires a command", name)
			}
		case "":
			return fmt.Errorf("manifest: handler %q has no type", name)
		default:
			return fmt.Errorf("manifest: handler %q has unknown type %q", name, h.Type)
		}
	}

	// Stage 4: cross-references.
	for integration := range m.Scopes {
		if !reservedIntegrations[integration] {
			return fmt.Errorf("manifest: scopes uses unknown integration %q (not in reserved set)", integration)
		}
		// Every scoped integration must have a matching identity field.
		switch integration {
		case "github":
			if m.Identity.GitHubUsername == "" {
				return fmt.Errorf("manifest: scopes.github requires identity.github_username")
			}
		case "slack":
			if m.Identity.SlackHandle == "" {
				return fmt.Errorf("manifest: scopes.slack requires identity.slack_handle")
			}
		case "linear":
			if m.Identity.LinearUser == "" {
				return fmt.Errorf("manifest: scopes.linear requires identity.linear_user")
			}
		case "email":
			if m.Identity.Email == "" {
				return fmt.Errorf("manifest: scopes.email requires identity.email")
			}
		}
	}

	referenced := map[string]bool{}
	for i, t := range m.Triggers {
		if _, ok := m.Handlers[t.Do]; !ok {
			return fmt.Errorf("manifest: triggers[%d].do=%q references undefined handler", i, t.Do)
		}
		referenced[t.Do] = true
	}
	for name := range m.Handlers {
		if !referenced[name] {
			return fmt.Errorf("manifest: handler %q is defined but never referenced by a trigger", name)
		}
	}

	return nil
}
