package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

func TestBumpVersion(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		kind    string
		want    string
		wantErr bool
	}{
		{"patch", "1.2.3", "patch", "1.2.4", false},
		{"patch_from_zero", "0.0.0", "patch", "0.0.1", false},
		{"minor", "1.2.3", "minor", "1.3.0", false},
		{"minor_resets_patch", "1.2.9", "minor", "1.3.0", false},
		{"major", "1.2.3", "major", "2.0.0", false},
		{"major_resets_minor_and_patch", "1.9.9", "major", "2.0.0", false},

		// Prerelease semantics (npm-compatible)
		{"prerelease_from_stable", "1.2.3", "prerelease", "1.2.4-0", false},
		{"prerelease_bump_numeric", "1.2.4-0", "prerelease", "1.2.4-1", false},
		{"prerelease_bump_dotted_numeric", "1.2.4-alpha.0", "prerelease", "1.2.4-alpha.1", false},
		{"prerelease_appends_to_non_numeric", "1.2.4-alpha", "prerelease", "1.2.4-alpha.0", false},

		// Explicit versions
		{"explicit_set", "1.2.3", "2.0.0", "2.0.0", false},
		{"explicit_with_prerelease", "1.2.3", "5.0.0-rc.1", "5.0.0-rc.1", false},

		// Errors
		{"unknown_kind", "1.2.3", "bogus", "", true},
		{"invalid_explicit", "1.2.3", "not-a-version", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := semver.MustParse(tc.from)
			got, err := bumpVersion(cur, tc.kind)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tc.want {
				t.Errorf("bump(%s, %s) = %s, want %s", tc.from, tc.kind, got, tc.want)
			}
		})
	}
}

// TestVersionBumpRoundTrip exercises the full Load → mutate → Save path
// against a real on-disk manifest, to ensure the new version survives a
// write/read cycle via the strict parser.
func TestVersionBumpRoundTrip(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "rt-test",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cur := semver.MustParse(m.Version)
	next, err := bumpVersion(cur, "minor")
	if err != nil {
		t.Fatalf("bumpVersion: %v", err)
	}
	m.Version = next.String()
	if err := m.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Version != "1.1.0" {
		t.Errorf("reloaded version = %q, want %q", reloaded.Version, "1.1.0")
	}
}
