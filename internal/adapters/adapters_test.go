package adapters

import (
	"sort"
	"testing"
	"testing/fstest"
)

func TestAllLoadsBundled(t *testing.T) {
	specs, err := All()
	if err != nil {
		t.Fatalf("All() error: %v", err)
	}
	wantNames := []string{"email", "github", "jira", "linear", "slack"}
	got := Names()
	sort.Strings(got)
	if len(got) != len(wantNames) {
		t.Fatalf("loaded %d adapters, want %d (%v)", len(got), len(wantNames), got)
	}
	for i, n := range wantNames {
		if got[i] != n {
			t.Errorf("Names()[%d] = %q, want %q", i, got[i], n)
		}
	}
	for _, n := range wantNames {
		s, ok := specs[n]
		if !ok {
			t.Errorf("missing %q in registry", n)
			continue
		}
		if s.Name != n {
			t.Errorf("%q: spec.Name = %q", n, s.Name)
		}
		if s.IdentityField == "" {
			t.Errorf("%q: identity_field is empty", n)
		}
		if len(s.Verbs) == 0 {
			t.Errorf("%q: verbs is empty", n)
		}
		if s.Status != "planned" {
			t.Errorf("%q: status = %q, want planned (all v0.1 adapters are stubs)", n, s.Status)
		}
	}
}

func TestLoadFromFSValidation(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name: "missing name",
			files: map[string]string{
				"x/adapter.yaml": `
description: test
identity_field: foo
verbs: [read]
status: planned
`,
			},
			wantErr: "adapter name is required",
		},
		{
			name: "missing identity_field",
			files: map[string]string{
				"x/adapter.yaml": `
name: x
verbs: [read]
status: planned
`,
			},
			wantErr: "no identity_field",
		},
		{
			name: "no verbs",
			files: map[string]string{
				"x/adapter.yaml": `
name: x
identity_field: foo
status: planned
`,
			},
			wantErr: "no verbs",
		},
		{
			name: "unknown status",
			files: map[string]string{
				"x/adapter.yaml": `
name: x
identity_field: foo
verbs: [read]
status: experimental
`,
			},
			wantErr: "unknown status",
		},
		{
			name: "unknown field is rejected (strict decoding)",
			files: map[string]string{
				"x/adapter.yaml": `
name: x
identity_field: foo
verbs: [read]
status: planned
extra_field: nope
`,
			},
			wantErr: "field extra_field not found",
		},
		{
			name: "duplicate adapter name",
			files: map[string]string{
				"a/adapter.yaml": `
name: dup
identity_field: foo
verbs: [read]
status: planned
`,
				"b/adapter.yaml": `
name: dup
identity_field: bar
verbs: [write]
status: planned
`,
			},
			wantErr: "defined twice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for path, body := range tc.files {
				fsys[path] = &fstest.MapFile{Data: []byte(body)}
			}
			_, err := loadFromFS(fsys)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
