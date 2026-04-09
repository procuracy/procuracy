// Package capability resolves manifest scope declarations into a typed
// capability set that adapters consume at construction time.
//
// The capability layer is the load-bearing security primitive of
// procuracy. When an adapter is built for a contractor, it is built
// from the contractor's resolved capability set — methods for ungranted
// verbs are *not constructed*, not "guarded with a permission check."
// The LLM cannot call a tool that does not exist; no clever prompt can
// invoke a method that was never wired up. This is capability-based
// security in the KeyKOS / Capsicum tradition, applied to AI agent
// tooling.
//
// See docs/manifest-spec.md §scopes and the README's "How it works"
// section for the conceptual model.
package capability

import (
	"fmt"
	"strings"
)

// Scopes is the unparsed scope declaration from a manifest:
// integration name → list of scope strings.
//
// This type is the input to Resolve. It is defined here (rather than in
// internal/manifest) because Resolve is the operation that interprets it
// — internal/manifest is just the YAML-shaped carrier.
type Scopes map[string][]string

// Scope is a single parsed scope string.
//
// Scope strings have the form "<verb>:<resource>" where <verb> is
// everything up to the LAST colon and <resource> is everything after.
// The last-colon rule is what allows multi-segment verbs like
// "pr:create" — the verb itself may contain colons, the resource may
// not (it is matched as a glob path, and globs do not need colons).
//
// A resource of literally "none" (case-sensitive) is the deny marker.
// "merge:none" parses as a Scope with Verb="merge" and Deny=true; the
// Resource field is empty for deny scopes.
type Scope struct {
	Verb     string
	Resource string
	Deny     bool
}

// ParseScope parses a single scope string. The empty string, strings
// without a colon, strings starting with a colon, and strings ending
// with a colon are all errors.
func ParseScope(s string) (Scope, error) {
	if s == "" {
		return Scope{}, fmt.Errorf("empty scope")
	}
	idx := strings.LastIndex(s, ":")
	if idx == -1 {
		return Scope{}, fmt.Errorf("scope %q: missing ':' (expected <verb>:<resource>)", s)
	}
	if idx == 0 {
		return Scope{}, fmt.Errorf("scope %q: empty verb", s)
	}
	if idx == len(s)-1 {
		return Scope{}, fmt.Errorf("scope %q: empty resource", s)
	}
	verb := s[:idx]
	resource := s[idx+1:]
	if resource == "none" {
		return Scope{Verb: verb, Deny: true}, nil
	}
	return Scope{Verb: verb, Resource: resource}, nil
}
