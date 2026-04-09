package capability

import (
	"strings"
	"testing"
)

// ----- ParseScope -----

func TestParseScope(t *testing.T) {
	cases := []struct {
		in       string
		wantVerb string
		wantRes  string
		wantDeny bool
	}{
		{"read:org/*", "read", "org/*", false},
		{"write:org/docs/**", "write", "org/docs/**", false},
		{"merge:none", "merge", "", true},
		{"pr:create:org/docs", "pr:create", "org/docs", false},
		{"pr:create:none", "pr:create", "", true},
		{"transition:project/eng/{Todo,InProgress,InReview,Done}", "transition", "project/eng/{Todo,InProgress,InReview,Done}", false},
		{"post:#engineering", "post", "#engineering", false},
		{"send:user@example.com", "send", "user@example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseScope(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Verb != tc.wantVerb {
				t.Errorf("Verb = %q, want %q", got.Verb, tc.wantVerb)
			}
			if got.Resource != tc.wantRes {
				t.Errorf("Resource = %q, want %q", got.Resource, tc.wantRes)
			}
			if got.Deny != tc.wantDeny {
				t.Errorf("Deny = %v, want %v", got.Deny, tc.wantDeny)
			}
		})
	}
}

func TestParseScopeErrors(t *testing.T) {
	cases := []struct {
		in      string
		wantSub string
	}{
		{"", "empty scope"},
		{"noverb", "missing ':'"},
		{":noverb", "empty verb"},
		{"verb:", "empty resource"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, err := ParseScope(tc.in)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

// ----- Match (glob) -----

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, input string
		want           bool
	}{
		// literal
		{"foo", "foo", true},
		{"foo", "bar", false},
		{"foo/bar", "foo/bar", true},
		{"foo/bar", "foo/baz", false},

		// "" matches only ""
		{"", "", true},
		{"", "foo", false},
		{"foo", "", false},

		// single-segment "*" — matches exactly one non-empty segment
		{"*", "foo", true},
		{"*", "anything-here", true},
		{"*", "", false}, // empty input has no segments to match
		{"*", "foo/bar", false},
		{"org/*", "org/foo", true},
		{"org/*", "org/foo/bar", false},
		{"org/*/docs", "org/aria/docs", true},
		{"org/*/docs", "org/aria/sub/docs", false},

		// "**" matches zero or more segments (Bash globstar semantics)
		{"**", "", true},
		{"**", "foo", true},
		{"**", "foo/bar/baz", true},
		{"org/**", "org", true}, // ** matches zero segments
		{"org/**", "org/foo", true},
		{"org/**", "org/foo/bar/baz", true},
		{"org/docs/**", "org/docs", true},
		{"org/docs/**", "org/docs/api/v1.md", true},
		{"org/docs/**", "org/src", false},
		{"**/docs", "docs", true}, // ** matches zero segments
		{"**/docs", "a/b/c/docs", true},
		{"**/docs", "docs/x", false},

		// mixed
		{"org/*/docs/**", "org/aria/docs", true}, // **=zero segments
		{"org/*/docs/**", "org/aria/docs/api/v1.md", true},
		{"org/*/docs/**", "org/aria/src/file.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"_vs_"+tc.input, func(t *testing.T) {
			if got := Match(tc.pattern, tc.input); got != tc.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.input, got, tc.want)
			}
		})
	}
}

// ----- Resolve -----

func TestResolveHappyPath(t *testing.T) {
	scopes := Scopes{
		"github": []string{"read:org/*", "write:org/docs/**", "merge:none"},
		"slack":  []string{"post:#engineering", "post:#aria-log", "dm:none"},
	}
	set, err := Resolve(scopes)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	gotInts := set.Integrations()
	wantInts := []string{"github", "slack"}
	if len(gotInts) != len(wantInts) {
		t.Fatalf("integrations = %v, want %v", gotInts, wantInts)
	}
	for i := range wantInts {
		if gotInts[i] != wantInts[i] {
			t.Errorf("integrations[%d] = %q, want %q", i, gotInts[i], wantInts[i])
		}
	}
}

func TestResolveUnknownIntegration(t *testing.T) {
	_, err := Resolve(Scopes{"frobnitz": {"read:foo"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown integration") {
		t.Errorf("error = %v, want 'unknown integration'", err)
	}
}

func TestResolveUnknownVerb(t *testing.T) {
	_, err := Resolve(Scopes{"github": {"frobnicate:org/*"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error should mention the bad verb: %v", err)
	}
	if !strings.Contains(err.Error(), "allowed verbs") {
		t.Errorf("error should list allowed verbs: %v", err)
	}
}

func TestResolveBadScopeString(t *testing.T) {
	_, err := Resolve(Scopes{"github": {"not-a-scope"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "scopes.github") {
		t.Errorf("error should be qualified with the integration: %v", err)
	}
}

// ----- Allows -----

func TestAllowsBasic(t *testing.T) {
	set, err := Resolve(Scopes{
		"github": []string{"read:org/*", "write:org/docs/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		integration, verb, resource string
		want                        bool
	}{
		{"github", "read", "org/aria", true},
		{"github", "read", "org/aria/sub", false}, // single-segment * does not match deeper
		{"github", "write", "org/docs/api.md", true},
		{"github", "write", "org/src/main.go", false},
		{"github", "merge", "org/docs", false},   // never granted
		{"slack", "post", "#engineering", false}, // integration not in this set
	}
	for _, tc := range cases {
		t.Run(tc.integration+"_"+tc.verb+"_"+tc.resource, func(t *testing.T) {
			if got := set.Allows(tc.integration, tc.verb, tc.resource); got != tc.want {
				t.Errorf("Allows(%q,%q,%q) = %v, want %v",
					tc.integration, tc.verb, tc.resource, got, tc.want)
			}
		})
	}
}

func TestAllowsDenyOverridesGrant(t *testing.T) {
	// Even if a granted pattern matches, an explicit deny on the same verb
	// short-circuits Allows. This is the README's "merge:none" property.
	set, err := Resolve(Scopes{
		"github": []string{"merge:org/*", "merge:none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.Allows("github", "merge", "org/anything") {
		t.Error("merge:none should override merge:org/*")
	}
}

func TestAllowsNilSet(t *testing.T) {
	var s *Set
	if s.Allows("github", "read", "org/foo") {
		t.Error("nil Set should never Allow")
	}
}

// ----- GrantedVerbs -----

func TestGrantedVerbs(t *testing.T) {
	set, err := Resolve(Scopes{
		"github": []string{"read:org/*", "write:org/docs/**", "merge:none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := set.GrantedVerbs("github")
	want := []string{"read", "write"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGrantedVerbsExcludesDenied(t *testing.T) {
	// merge appears as both grant and deny — deny wins, so merge is NOT
	// in the granted list.
	set, err := Resolve(Scopes{
		"github": []string{"read:org/*", "merge:org/*", "merge:none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := set.GrantedVerbs("github")
	for _, v := range got {
		if v == "merge" {
			t.Error("merge should not appear in GrantedVerbs when explicitly denied")
		}
	}
}

func TestDeniedVerbs(t *testing.T) {
	set, err := Resolve(Scopes{
		"github": []string{"read:org/*", "merge:none"},
		"slack":  []string{"post:#general", "dm:none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := set.DeniedVerbs("github"); len(got) != 1 || got[0] != "merge" {
		t.Errorf("github denied = %v, want [merge]", got)
	}
	if got := set.DeniedVerbs("slack"); len(got) != 1 || got[0] != "dm" {
		t.Errorf("slack denied = %v, want [dm]", got)
	}
}
