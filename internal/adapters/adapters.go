// Package adapters defines the integration adapter registry.
//
// Each adapter ships a small `adapter.yaml` manifest that declares its
// identity binding (which Identity struct field it requires), the verbs
// it allows in scope strings, and its implementation status. The registry
// loads all bundled adapter manifests at startup via embed.FS and exposes
// them to the manifest validator.
//
// Adding a new adapter is a matter of dropping a new YAML file under
// internal/adapters/<name>/adapter.yaml — no Go code changes required.
// This is the v0.1 "open adapter registration mechanism" the
// docs/enterprise-provisioning.md design note locks in as a non-breaking
// constraint for v0.2.
package adapters

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Spec is the parsed contents of a single adapter.yaml file.
//
// IdentityField is the name of the field in the manifest's identity:
// block that this adapter requires (e.g. "github_username"). Verbs is
// the closed set of scope verbs this adapter recognizes; an "unknown
// verb" check is delegated to the capability layer (not in v0.1) but
// the field is parsed now so the adapter manifests are forward-compatible.
type Spec struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	IdentityField string   `yaml:"identity_field"`
	Verbs         []string `yaml:"verbs"`
	Status        string   `yaml:"status"` // planned | alpha | stable
}

//go:embed all:github all:slack all:linear all:jira all:email
var bundled embed.FS

var (
	loadOnce sync.Once
	loaded   map[string]Spec
	loadErr  error
)

// All returns the registry of bundled adapters keyed by name. The
// registry is loaded once on first call and cached for the lifetime of
// the process. If any bundled manifest fails to parse, All returns the
// (parse) error on every call until the binary is restarted — this is
// intentional, because a malformed bundled adapter is a build-time bug,
// not a runtime condition to recover from.
func All() (map[string]Spec, error) {
	loadOnce.Do(func() {
		loaded, loadErr = loadFromFS(bundled)
	})
	return loaded, loadErr
}

// Names returns the registered adapter names in deterministic order.
// Useful for error messages that list "valid integrations are: ...".
func Names() []string {
	specs, err := All()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(specs))
	for n := range specs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// loadFromFS walks the given filesystem looking for files named
// adapter.yaml and parses each one into a Spec. The directory name
// is not used as the adapter name — the name comes from inside the
// YAML — so the on-disk layout is purely organizational.
func loadFromFS(fsys fs.FS) (map[string]Spec, error) {
	out := map[string]Spec{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Base(p) != "adapter.yaml" {
			return nil
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		var spec Spec
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(&spec); err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		if err := validateSpec(spec, p); err != nil {
			return err
		}
		if existing, dup := out[spec.Name]; dup {
			return fmt.Errorf("adapter %q defined twice (%s and %s collide)", spec.Name, p, existing.Name)
		}
		out[spec.Name] = spec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func validateSpec(s Spec, path string) error {
	if s.Name == "" {
		return fmt.Errorf("%s: adapter name is required", path)
	}
	if s.IdentityField == "" {
		return fmt.Errorf("%s: adapter %q has no identity_field", path, s.Name)
	}
	if len(s.Verbs) == 0 {
		return fmt.Errorf("%s: adapter %q has no verbs", path, s.Name)
	}
	switch s.Status {
	case "planned", "alpha", "stable":
	case "":
		return fmt.Errorf("%s: adapter %q has no status", path, s.Name)
	default:
		return fmt.Errorf("%s: adapter %q has unknown status %q", path, s.Name, s.Status)
	}
	return nil
}
