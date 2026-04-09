package capability

import (
	"fmt"
	"sort"

	"github.com/procuracy/procuracy/internal/adapters"
)

// Set is the resolved capability set for a contractor: per integration,
// which verbs are granted (and against what resource patterns) and which
// are explicitly denied.
//
// Denials always win over grants. Calling Allows for a denied verb
// returns false even if the verb has a matching grant pattern. This is
// the property the README's "merge:none" example relies on — a deny is
// not advisory, it is structurally enforced.
//
// In v0.1 the Set is consumed only by the validate command (Resolve is
// stage 5 of manifest validation). In v0.2 it will also be passed to
// adapter constructors, which use GrantedVerbs to decide which methods
// to expose at construction time.
type Set struct {
	integrations map[string]*integrationCaps
}

type integrationCaps struct {
	granted map[string][]string // verb → list of resource patterns
	denied  map[string]bool     // verb → true if explicitly denied
}

// Resolve parses and validates a Scopes map against the bundled adapter
// registry.
//
// Each scope string is parsed via ParseScope. Each verb is checked
// against the verbs declared by the matching adapter manifest; unknown
// verbs are an error and the error message lists the allowed verbs so
// typos are obvious. Deny markers (<verb>:none) are recorded; explicit
// grants are recorded with their resource patterns.
//
// The returned Set is safe to use; any error means it is not.
func Resolve(scopes Scopes) (*Set, error) {
	registry, err := adapters.All()
	if err != nil {
		return nil, fmt.Errorf("capability: load adapter registry: %w", err)
	}
	set := &Set{integrations: map[string]*integrationCaps{}}
	for integration, scopeStrs := range scopes {
		spec, ok := registry[integration]
		if !ok {
			return nil, fmt.Errorf("capability: scopes uses unknown integration %q (registered adapters: %v)",
				integration, adapters.Names())
		}
		validVerbs := map[string]bool{}
		for _, v := range spec.Verbs {
			validVerbs[v] = true
		}
		ic := &integrationCaps{
			granted: map[string][]string{},
			denied:  map[string]bool{},
		}
		for _, ss := range scopeStrs {
			scope, err := ParseScope(ss)
			if err != nil {
				return nil, fmt.Errorf("capability: scopes.%s: %w", integration, err)
			}
			if !validVerbs[scope.Verb] {
				return nil, fmt.Errorf("capability: scopes.%s: verb %q is not recognized by the %s adapter (allowed verbs: %v)",
					integration, scope.Verb, integration, spec.Verbs)
			}
			if scope.Deny {
				ic.denied[scope.Verb] = true
				continue
			}
			ic.granted[scope.Verb] = append(ic.granted[scope.Verb], scope.Resource)
		}
		set.integrations[integration] = ic
	}
	return set, nil
}

// Allows reports whether the given (integration, verb, resource) tuple
// is permitted by this capability set.
//
// Returns false if any of:
//   - the integration was never declared in the manifest
//   - the verb is explicitly denied (<verb>:none)
//   - no granted resource pattern for that verb matches the resource
//
// Allows is intentionally cheap and pure — it does no I/O, takes no
// locks, and is safe for concurrent use. Adapters are expected to call
// it on every request as the last gate before invoking the underlying
// API. The capability *list* (GrantedVerbs) is the structural surface;
// Allows is the per-call check on top of that surface.
func (s *Set) Allows(integration, verb, resource string) bool {
	if s == nil {
		return false
	}
	ic, ok := s.integrations[integration]
	if !ok {
		return false
	}
	if ic.denied[verb] {
		return false
	}
	for _, p := range ic.granted[verb] {
		if Match(p, resource) {
			return true
		}
	}
	return false
}

// GrantedVerbs returns the closed list of verbs that are granted (and
// not denied) for the given integration. The slice is sorted for
// determinism.
//
// This is the *structural* surface area an adapter exposes — verbs not
// in the list are unreachable, even via prompt injection or method
// introspection, because the corresponding methods are not constructed
// in the first place. Adapter constructors call this at build time and
// return an object that only has the granted methods.
//
// A verb that has both a grant and a deny does NOT appear in the
// returned list (the deny wins). A verb with only a deny also does
// not appear (you cannot deny something you never granted).
func (s *Set) GrantedVerbs(integration string) []string {
	if s == nil {
		return nil
	}
	ic, ok := s.integrations[integration]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(ic.granted))
	for verb := range ic.granted {
		if !ic.denied[verb] {
			out = append(out, verb)
		}
	}
	sort.Strings(out)
	return out
}

// DeniedVerbs returns the sorted list of verbs that were explicitly
// denied for the given integration. Useful for diagnostics ("why was
// this call rejected") and for surfacing denials in the audit log.
func (s *Set) DeniedVerbs(integration string) []string {
	if s == nil {
		return nil
	}
	ic, ok := s.integrations[integration]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(ic.denied))
	for verb := range ic.denied {
		out = append(out, verb)
	}
	sort.Strings(out)
	return out
}

// Integrations returns the sorted list of integrations referenced by
// this set. Adapter constructors iterate over this list to know which
// adapters to build.
func (s *Set) Integrations() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.integrations))
	for k := range s.integrations {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
