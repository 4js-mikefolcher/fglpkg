package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
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

// TestBumpCommandRoundTrip exercises the `bump` command end-to-end against a
// real on-disk manifest (no --git): Load → mutate → Save, confirming the new
// version survives a write/read cycle through the strict parser (GIS-288).
func TestBumpCommandRoundTrip(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "rt-test",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if _, err := captureStdout(t, func() error { return cmdBump([]string{"minor"}) }); err != nil {
		t.Fatalf("cmdBump: %v", err)
	}

	reloaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Version != "1.1.0" {
		t.Errorf("reloaded version = %q, want %q", reloaded.Version, "1.1.0")
	}
}

// TestBumpUpdatesLockfileRootVersion: when a lockfile sits beside the manifest,
// `fglpkg bump` updates its root.version to match — the way npm's `npm version`
// also rewrites package-lock.json (GIS-492). Only root.version changes; the
// root.declared snapshot (and the resolved entries) must survive, because a
// version bump is not a re-resolution.
func TestBumpUpdatesLockfileRootVersion(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "lock-sync",
  "version": "1.2.3",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup manifest: %v", err)
	}
	lock := `{
  "lockfileVersion": 1,
  "generatedAt": "2026-01-01T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": {
    "name": "lock-sync",
    "version": "1.2.3",
    "declared": { "dependencies": {}, "devDependencies": {}, "optionalDependencies": {} }
  },
  "packages": [],
  "jars": []
}`
	if err := os.WriteFile(filepath.Join(dir, lockfile.Filename), []byte(lock), 0644); err != nil {
		t.Fatalf("setup lock: %v", err)
	}

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if _, err := captureStdout(t, func() error { return cmdBump([]string{"patch"}) }); err != nil {
		t.Fatalf("cmdBump: %v", err)
	}

	lf, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if lf.RootManifest.Version != "1.2.4" {
		t.Errorf("lock root.version = %q, want %q", lf.RootManifest.Version, "1.2.4")
	}
	if lf.RootManifest.Declared == nil {
		t.Error("lock root.declared was dropped; bump must not re-resolve")
	}
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if m.Version != "1.2.4" {
		t.Errorf("manifest version = %q, want %q", m.Version, "1.2.4")
	}
}

// TestBumpWithoutLockfileCreatesNone: `fglpkg bump` in a project that has no
// lockfile must not fabricate one — bump never resolves (GIS-492).
func TestBumpWithoutLockfileCreatesNone(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "no-lock",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if _, err := captureStdout(t, func() error { return cmdBump([]string{"minor"}) }); err != nil {
		t.Fatalf("cmdBump: %v", err)
	}
	if lockfile.Exists(dir) {
		t.Error("bump created a lockfile where none existed; it must not resolve")
	}
}

// TestBumpRequiresKind: `fglpkg bump` with no bump kind is a usage error.
func TestBumpRequiresKind(t *testing.T) {
	err := cmdBump(nil)
	if err == nil {
		t.Fatal("expected usage error for bare `bump`, got nil")
	}
	if !strings.Contains(err.Error(), "usage: fglpkg bump") {
		t.Errorf("error = %q, want a bump usage message", err.Error())
	}
}

// TestVersionRejectsBumpKind: `fglpkg version patch` (and any argument) is now
// rejected and points the user at `fglpkg bump` (GIS-288). The manifest must
// not be touched.
func TestVersionRejectsBumpKind(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "rt-test",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	err := cmdVersion([]string{"patch"})
	if err == nil {
		t.Fatal("expected `version patch` to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "fglpkg bump") {
		t.Errorf("error = %q, want it to redirect to `fglpkg bump`", err.Error())
	}
	// The manifest must be untouched.
	reloaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Version != "1.0.0" {
		t.Errorf("version changed to %q; `version <kind>` must not mutate the manifest", reloaded.Version)
	}
}

// TestVersionPrintsToolVersion: bare `fglpkg version` prints the tool banner.
func TestVersionPrintsToolVersion(t *testing.T) {
	out, err := captureStdout(t, func() error { return cmdVersion(nil) })
	if err != nil {
		t.Fatalf("cmdVersion: %v", err)
	}
	if !strings.Contains(out, "fglpkg version") {
		t.Errorf("output %q missing tool-version banner", out)
	}
}

// TestVersionFlags: `fglpkg --version` and `fglpkg -v` print the tool version
// via the top-level flag path in Execute (GIS-288).
func TestVersionFlags(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	for _, flag := range []string{"--version", "-v"} {
		t.Run(flag, func(t *testing.T) {
			out, err := captureStdout(t, func() error {
				os.Args = []string{"fglpkg", flag}
				return Execute()
			})
			if err != nil {
				t.Fatalf("Execute(%s): %v", flag, err)
			}
			if !strings.Contains(out, "fglpkg version") {
				t.Errorf("%s output %q missing tool-version banner", flag, out)
			}
		})
	}
}
