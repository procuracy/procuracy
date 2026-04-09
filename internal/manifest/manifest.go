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

	"github.com/procuracy/procuracy/internal/adapters"
	"github.com/procuracy/procuracy/internal/capability"
)

// Manifest is the root of a procuracy.yaml file.
type Manifest struct {
	Name          string             `yaml:"name"`
	DisplayName   string             `yaml:"display_name,omitempty"`
	Description   string             `yaml:"description,omitempty"`
	Identity      Identity           `yaml:"identity"`
	Scopes        Scopes             `yaml:"scopes"`
	Triggers      []Trigger          `yaml:"triggers"`
	Runtime       Runtime            `yaml:"runtime"`
	Handlers      map[string]Handler `yaml:"handlers"`
	Observability *Observability     `yaml:"observability,omitempty"`
	Termination   *Termination       `yaml:"termination,omitempty"`
	State         *State             `yaml:"state,omitempty"`
}

// Identity is the contractor's account presence on each integration.
//
// The Mode field selects between procuracy's two identity provisioning
// models: "direct" (the v0.1 default — operator holds OAuth tokens and
// procuracy creates accounts via direct API calls) and "idp-managed"
// (the v0.2 target — procuracy orchestrates an identity provider and
// lets SCIM cascade into downstream tools). The latter mode parses
// successfully but produces a warning at validate time and is rejected
// by the v0.1 runtime; see docs/enterprise-provisioning.md for the
// full design.
type Identity struct {
	Mode           IdentityMode `yaml:"mode,omitempty"`
	Email          string       `yaml:"email,omitempty"`
	GitHubUsername string       `yaml:"github_username,omitempty"`
	SlackHandle    string       `yaml:"slack_handle,omitempty"`
	LinearUser     string       `yaml:"linear_user,omitempty"`
	JiraUser       string       `yaml:"jira_user,omitempty"`
}

// IdentityMode selects between provisioning models.
type IdentityMode string

const (
	// IdentityModeDirect is the v0.1 model: one operator with OAuth tokens.
	IdentityModeDirect IdentityMode = "direct"
	// IdentityModeIdPManaged is the v0.2 model: procuracy orchestrates the
	// IdP and lets SCIM cascade. Parses successfully in v0.1 (so manifests
	// targeted at v0.2 do not fail validation today) but is rejected by the
	// v0.1 runtime with a pointer to docs/enterprise-provisioning.md.
	IdentityModeIdPManaged IdentityMode = "idp-managed"
)

// fieldByName returns the value of the named identity field. The name
// must match an adapter manifest's identity_field declaration. Used by
// the cross-reference checker to look up the identity required by an
// adapter without hard-coding the adapter list in this package.
func (i *Identity) fieldByName(name string) (value string, known bool) {
	switch name {
	case "email":
		return i.Email, true
	case "github_username":
		return i.GitHubUsername, true
	case "slack_handle":
		return i.SlackHandle, true
	case "linear_user":
		return i.LinearUser, true
	case "jira_user":
		return i.JiraUser, true
	}
	return "", false
}

// Scopes is the capability declaration. Keys are integration names;
// values are lists of scope strings (see docs/manifest-spec.md §scopes).
//
// The type is owned by internal/capability — that is the package that
// interprets it. This alias keeps the historical name available on the
// manifest package for callers who think of scopes as part of the
// manifest schema.
type Scopes = capability.Scopes

// Trigger describes when the contractor wakes up.
type Trigger struct {
	On    string `yaml:"on"`
	Where string `yaml:"where,omitempty"`
	Cron  string `yaml:"cron,omitempty"`
	Do    string `yaml:"do"`
}

// Runtime describes how the contractor thinks and what it costs.
type Runtime struct {
	Engine              string  `yaml:"engine"`
	Model               string  `yaml:"model,omitempty"`
	Workspace           string  `yaml:"workspace"`
	CostLimitDailyUSD   float64 `yaml:"cost_limit_daily_usd"`
	CostLimitPerTaskUSD float64 `yaml:"cost_limit_per_task_usd"`
	TimeoutPerTaskSecs  int     `yaml:"timeout_per_task_seconds,omitempty"`
	MaxConcurrentTasks  int     `yaml:"max_concurrent_tasks,omitempty"`
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

// State tracks where a manifest is in its provisioning lifecycle.
//
// In v0.1 the State block is parsed and round-tripped but is otherwise
// ignored by the runtime — `procuracy validate` does not write to it,
// `procuracy hire` does not consult it. The block is defined in v0.1 so
// that v0.2's three-actor request → approve → provision flow has a
// place to land without requiring a breaking spec change. See
// docs/enterprise-provisioning.md §5.1 for the full v0.2 design.
type State struct {
	Phase          StatePhase `yaml:"phase,omitempty"`
	RequestedBy    string     `yaml:"requested_by,omitempty"`
	ApprovedBy     string     `yaml:"approved_by,omitempty"`
	ProvisionedBy  string     `yaml:"provisioned_by,omitempty"`
	ApprovalTicket string     `yaml:"approval_ticket,omitempty"`
	Signature      string     `yaml:"signature,omitempty"`
	History        []string   `yaml:"history,omitempty"`
}

// StatePhase tracks the manifest's position in the provisioning lifecycle.
type StatePhase string

const (
	StatePhaseDraft       StatePhase = "draft"
	StatePhaseRequested   StatePhase = "requested"
	StatePhaseApproved    StatePhase = "approved"
	StatePhaseProvisioned StatePhase = "provisioned"
	StatePhaseRunning     StatePhase = "running"
	StatePhasePaused      StatePhase = "paused"
	StatePhaseFired       StatePhase = "fired"
)

var validStatePhases = map[StatePhase]bool{
	StatePhaseDraft:       true,
	StatePhaseRequested:   true,
	StatePhaseApproved:    true,
	StatePhaseProvisioned: true,
	StatePhaseRunning:     true,
	StatePhasePaused:      true,
	StatePhaseFired:       true,
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
	// Identity mode defaults to direct if unset.
	if m.Identity.Mode == "" {
		m.Identity.Mode = IdentityModeDirect
	}
	switch m.Identity.Mode {
	case IdentityModeDirect, IdentityModeIdPManaged:
		// ok
	default:
		return fmt.Errorf("manifest: identity.mode %q is not recognized (use %q or %q)",
			m.Identity.Mode, IdentityModeDirect, IdentityModeIdPManaged)
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
	if m.State != nil && m.State.Phase != "" && !validStatePhases[m.State.Phase] {
		return fmt.Errorf("manifest: state.phase %q is not recognized", m.State.Phase)
	}

	// Stage 4: cross-references against the adapter registry.
	registry, err := adapters.All()
	if err != nil {
		return fmt.Errorf("manifest: load adapter registry: %w", err)
	}
	for integration := range m.Scopes {
		spec, ok := registry[integration]
		if !ok {
			return fmt.Errorf("manifest: scopes uses unknown integration %q (registered adapters: %v)",
				integration, adapters.Names())
		}
		// Every scoped integration must have a populated identity field
		// matching what the adapter declared it needs.
		val, known := m.Identity.fieldByName(spec.IdentityField)
		if !known {
			// The adapter declared an identity_field that the Identity
			// struct does not know about. This is a build-time bug in
			// the adapter manifest, not a user error.
			return fmt.Errorf("manifest: adapter %q declares identity_field=%q but the parser does not know that field (likely an adapter manifest bug)",
				integration, spec.IdentityField)
		}
		if val == "" {
			return fmt.Errorf("manifest: scopes.%s requires identity.%s", integration, spec.IdentityField)
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

	// Stage 5: capability resolution. Parse every scope string, validate
	// every verb against its adapter's declared verb set, record deny
	// markers, and produce a Set the runtime can later pass to adapter
	// constructors. The Set itself is discarded here — Validate is
	// concerned only with whether resolution succeeds. The runtime will
	// re-Resolve when it actually constructs adapters.
	if _, err := capability.Resolve(m.Scopes); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}

	return nil
}

// Warnings returns non-fatal advisory messages about the manifest.
//
// Warnings are emitted for v0.2 features that the manifest declares but
// the v0.1 runtime cannot honor — currently identity.mode=idp-managed
// and any populated state block. They are not errors: validation still
// passes. The intent is to let users author manifests targeted at the
// v0.2 design today without breaking the v0.1 validate command.
func (m *Manifest) Warnings() []string {
	var ws []string
	if m.Identity.Mode == IdentityModeIdPManaged {
		ws = append(ws, "identity.mode=idp-managed is parsed but not implemented in v0.1; the v0.1 runtime will refuse to hire this contractor. See docs/enterprise-provisioning.md §5.2.")
	}
	if m.State != nil && (m.State.Phase != "" || m.State.RequestedBy != "" || m.State.ApprovedBy != "" || m.State.ProvisionedBy != "" || m.State.ApprovalTicket != "" || m.State.Signature != "" || len(m.State.History) > 0) {
		ws = append(ws, "state block is defined but ignored by the v0.1 runtime; v0.2 will populate it via procuracy request/approve/hire. See docs/enterprise-provisioning.md §5.1.")
	}
	return ws
}
