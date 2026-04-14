package manifest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/procuracy/procuracy/internal/capability"
	"gopkg.in/yaml.v3"
)

// Group defines a reusable scope profile that multiple contractors
// can reference. Groups are defined in a groups.yaml file (typically
// in the same directory as the contractor manifests) and are owned
// by the team lead or security team. Changing a group is one PR that
// affects every contractor referencing it.
type Group struct {
	Description         string            `yaml:"description,omitempty"`
	Scopes              capability.Scopes `yaml:"scopes,omitempty"`
	CostLimitDailyUSD   *float64          `yaml:"cost_limit_daily_usd,omitempty"`
	CostLimitPerTaskUSD *float64          `yaml:"cost_limit_per_task_usd,omitempty"`
	Notifications       *Notifications    `yaml:"notifications,omitempty"`
}

// Groups is the top-level map in a groups.yaml file: group name → definition.
type Groups map[string]Group

// LoadGroups reads and parses a groups.yaml file. Returns an empty map
// (not an error) if the file does not exist — groups are optional.
func LoadGroups(path string) (Groups, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Groups{}, nil
		}
		return nil, fmt.Errorf("read groups: %w", err)
	}
	return ParseGroups(raw)
}

// ParseGroups decodes raw YAML into a Groups map and validates each group.
func ParseGroups(raw []byte) (Groups, error) {
	var g Groups
	if err := yaml.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("parse groups: %w", err)
	}
	for name, group := range g {
		if err := validateGroup(name, group); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func validateGroup(name string, g Group) error {
	if g.CostLimitDailyUSD != nil && *g.CostLimitDailyUSD <= 0 {
		return fmt.Errorf("group %q: cost_limit_daily_usd must be > 0", name)
	}
	if g.CostLimitPerTaskUSD != nil && *g.CostLimitPerTaskUSD <= 0 {
		return fmt.Errorf("group %q: cost_limit_per_task_usd must be > 0", name)
	}
	if g.CostLimitDailyUSD != nil && g.CostLimitPerTaskUSD != nil &&
		*g.CostLimitPerTaskUSD > *g.CostLimitDailyUSD {
		return fmt.Errorf("group %q: cost_limit_per_task_usd (%.2f) must be <= cost_limit_daily_usd (%.2f)",
			name, *g.CostLimitPerTaskUSD, *g.CostLimitDailyUSD)
	}
	return nil
}

// ApplyGroup resolves a manifest's `group` field by merging the group's
// values into the manifest. The merge rules are:
//
//   - Scopes: group scopes are used if the manifest has no scopes.
//     If the manifest declares its own scopes, they win (full override,
//     not merge — you either use the group's scopes or define your own).
//   - CostLimitDailyUSD: group value used if manifest's is zero.
//   - CostLimitPerTaskUSD: group value used if manifest's is zero.
//   - Notifications: group notifications used if manifest has none.
//     If the manifest has its own, they win.
//
// This is intentionally simple: groups provide defaults, manifests
// override. No deep merging, no field-level mixing. One place to look
// to understand what a contractor got from its group.
func ApplyGroup(m *Manifest, groups Groups) error {
	if m.Group == "" {
		return nil
	}
	g, ok := groups[m.Group]
	if !ok {
		return fmt.Errorf("manifest %q references group %q but it is not defined in groups.yaml", m.Name, m.Group)
	}

	// Scopes: group provides if manifest has none.
	if len(m.Scopes) == 0 && len(g.Scopes) > 0 {
		m.Scopes = g.Scopes
	}

	// Cost limits: group provides if manifest's are zero.
	if m.Runtime.CostLimitDailyUSD == 0 && g.CostLimitDailyUSD != nil {
		m.Runtime.CostLimitDailyUSD = *g.CostLimitDailyUSD
	}
	if m.Runtime.CostLimitPerTaskUSD == 0 && g.CostLimitPerTaskUSD != nil {
		m.Runtime.CostLimitPerTaskUSD = *g.CostLimitPerTaskUSD
	}

	// Notifications: group provides if manifest has none.
	if m.Notifications == nil && g.Notifications != nil {
		m.Notifications = g.Notifications
	}

	return nil
}

// LoadAndApplyGroups is a convenience function that loads groups.yaml
// from the given directory (if it exists) and applies the group to the
// manifest. This is the function that `procuracy run` and `procuracy
// validate` call.
func LoadAndApplyGroups(m *Manifest, dir string) error {
	groupsPath := filepath.Join(dir, "groups.yaml")
	groups, err := LoadGroups(groupsPath)
	if err != nil {
		return err
	}
	return ApplyGroup(m, groups)
}
