package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// ─── Bin field ───────────────────────────────────────────────────────────────

func TestBinFieldRoundTrip(t *testing.T) {
	m := manifest.New("testpkg", "1.0.0", "test", "author")
	m.Bin = map[string]string{
		"migrate": "scripts/migrate.sh",
		"seed":    "scripts/seed.py",
	}

	dir := t.TempDir()
	if err := m.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Bin) != 2 {
		t.Fatalf("expected 2 bin entries, got %d", len(loaded.Bin))
	}
	if loaded.Bin["migrate"] != "scripts/migrate.sh" {
		t.Errorf("expected migrate -> scripts/migrate.sh, got %q", loaded.Bin["migrate"])
	}
	if loaded.Bin["seed"] != "scripts/seed.py" {
		t.Errorf("expected seed -> scripts/seed.py, got %q", loaded.Bin["seed"])
	}
}

func TestBinFilesDeduplication(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Bin: map[string]string{
			"cmd1": "scripts/shared.sh",
			"cmd2": "scripts/shared.sh",
			"cmd3": "scripts/other.sh",
		},
	}
	files := m.BinFiles()
	if len(files) != 2 {
		t.Fatalf("expected 2 unique files, got %d: %v", len(files), files)
	}
}

func TestBinFilesEmpty(t *testing.T) {
	m := &manifest.Manifest{Name: "test", Version: "1.0.0"}
	files := m.BinFiles()
	if files != nil {
		t.Fatalf("expected nil, got %v", files)
	}
}

func TestBinFilesSorted(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Bin: map[string]string{
			"z": "scripts/z.sh",
			"a": "scripts/a.sh",
			"m": "scripts/m.sh",
		},
	}
	files := m.BinFiles()
	for i := 1; i < len(files); i++ {
		if files[i] < files[i-1] {
			t.Fatalf("BinFiles not sorted: %v", files)
		}
	}
}

// ─── Docs field ──────────────────────────────────────────────────────────────

func TestDocsFieldRoundTrip(t *testing.T) {
	m := manifest.New("testpkg", "1.0.0", "test", "author")
	m.Docs = []string{"README.md", "docs/**/*.md"}

	dir := t.TempDir()
	if err := m.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Docs) != 2 {
		t.Fatalf("expected 2 docs patterns, got %d", len(loaded.Docs))
	}
	if loaded.Docs[0] != "README.md" {
		t.Errorf("expected README.md, got %q", loaded.Docs[0])
	}
	if loaded.Docs[1] != "docs/**/*.md" {
		t.Errorf("expected docs/**/*.md, got %q", loaded.Docs[1])
	}
}

// ─── omitempty ───────────────────────────────────────────────────────────────

func TestOmitEmptyBinDocs(t *testing.T) {
	m := manifest.New("testpkg", "1.0.0", "", "")
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if contains(s, `"bin"`) {
		t.Errorf("expected bin to be omitted, got: %s", s)
	}
	if contains(s, `"docs"`) {
		t.Errorf("expected docs to be omitted, got: %s", s)
	}
}

// ─── Validation ──────────────────────────────────────────────────────────────

func TestValidateBinEmptyCommandName(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Bin:     map[string]string{"": "scripts/run.sh"},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for empty bin command name")
	}
}

func TestValidateBinPathSeparatorInCommand(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Bin:     map[string]string{"sub/cmd": "scripts/run.sh"},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for path separator in bin command name")
	}
}

func TestValidateBinEmptyScriptPath(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Bin:     map[string]string{"cmd": ""},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for empty bin script path")
	}
}

func TestValidateBinAbsoluteScriptPath(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Bin:     map[string]string{"cmd": "/usr/local/bin/script"},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for absolute bin script path")
	}
}

func TestValidateDocsInvalidPattern(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Docs:    []string{"[invalid"},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for invalid docs glob pattern")
	}
}

func TestValidateBinValid(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Bin:     map[string]string{"migrate": "scripts/migrate.sh"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateDocsValid(t *testing.T) {
	m := &manifest.Manifest{
		Name:    "test",
		Version: "1.0.0",
		Docs:    []string{"README.md", "docs/**/*.md"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

// ─── Save/Load round-trip with both fields ──────────────────────────────────

func TestSaveLoadWithBinAndDocs(t *testing.T) {
	m := manifest.New("fullpkg", "2.0.0", "full test", "tester")
	m.Bin = map[string]string{"run-it": "bin/run.sh"}
	m.Docs = []string{"*.md"}

	dir := t.TempDir()
	if err := m.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file contains both fields.
	data, _ := os.ReadFile(filepath.Join(dir, "fglpkg.json"))
	s := string(data)
	if !contains(s, `"bin"`) {
		t.Error("saved JSON missing bin field")
	}
	if !contains(s, `"docs"`) {
		t.Error("saved JSON missing docs field")
	}

	loaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Bin["run-it"] != "bin/run.sh" {
		t.Errorf("bin mismatch: %v", loaded.Bin)
	}
	if len(loaded.Docs) != 1 || loaded.Docs[0] != "*.md" {
		t.Errorf("docs mismatch: %v", loaded.Docs)
	}
}

// ─── Strict parsing ──────────────────────────────────────────────────────────

func TestLoadRejectsUnknownTopLevelField(t *testing.T) {
	dir := t.TempDir()
	raw := `{"name":"x","version":"1.0.0","typoField":true}`
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(raw), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := manifest.Load(dir)
	if err == nil {
		t.Fatal("expected error for unknown top-level field, got nil")
	}
	if !contains(err.Error(), "typoField") {
		t.Errorf("error should mention the unknown field name, got: %v", err)
	}
}

func TestLoadRejectsFlatDependencies(t *testing.T) {
	dir := t.TempDir()
	raw := `{
		"name": "x",
		"version": "1.0.0",
		"dependencies": {
			"restdblib": ">=1.0.0"
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(raw), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := manifest.Load(dir)
	if err == nil {
		t.Fatal("expected error for flat dependencies, got nil")
	}
	msg := err.Error()
	if !contains(msg, "restdblib") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
	if !contains(msg, "dependencies.fgl.restdblib") {
		t.Errorf("error should suggest the correct nesting, got: %v", err)
	}
}

func TestLoadAcceptsNestedFGLDependencies(t *testing.T) {
	dir := t.TempDir()
	raw := `{
		"name": "x",
		"version": "1.0.0",
		"dependencies": {
			"fgl": { "restdblib": ">=1.0.0" }
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(raw), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Dependencies.FGL["restdblib"] != ">=1.0.0" {
		t.Errorf("expected restdblib >=1.0.0, got %v", m.Dependencies.FGL)
	}
}

// ─── Scopes: dev + optional ──────────────────────────────────────────────────

func TestScopedDependenciesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	raw := `{
		"name": "x",
		"version": "1.0.0",
		"dependencies": { "fgl": { "core": "^1.0.0" } },
		"devDependencies": { "fgl": { "tester": "^0.1.0" } },
		"optionalDependencies": { "fgl": { "telemetry": "^2.0.0" } }
	}`
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(raw), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Dependencies.FGL["core"] != "^1.0.0" {
		t.Errorf("prod dep missing: %v", m.Dependencies.FGL)
	}
	if m.DevDependencies.FGL["tester"] != "^0.1.0" {
		t.Errorf("dev dep missing: %v", m.DevDependencies.FGL)
	}
	if m.OptionalDependencies.FGL["telemetry"] != "^2.0.0" {
		t.Errorf("optional dep missing: %v", m.OptionalDependencies.FGL)
	}
}

// Adding a dep under one scope must remove it from any other scope it used to
// live in, so the same name never appears in two buckets.
func TestAddFGLDependencyScopedMovesBetweenScopes(t *testing.T) {
	m := manifest.New("x", "1.0.0", "", "")
	m.AddFGLDependencyScoped("foo", "^1.0.0", manifest.ScopeDev)
	if _, ok := m.DevDependencies.FGL["foo"]; !ok {
		t.Fatal("expected foo in dev")
	}
	m.AddFGLDependencyScoped("foo", "^1.0.0", manifest.ScopeProd)
	if _, ok := m.DevDependencies.FGL["foo"]; ok {
		t.Error("expected foo removed from dev after moving to prod")
	}
	if _, ok := m.Dependencies.FGL["foo"]; !ok {
		t.Error("expected foo in prod")
	}
	m.AddFGLDependencyScoped("foo", "^1.0.0", manifest.ScopeOptional)
	if _, ok := m.Dependencies.FGL["foo"]; ok {
		t.Error("expected foo removed from prod after moving to optional")
	}
	if _, ok := m.OptionalDependencies.FGL["foo"]; !ok {
		t.Error("expected foo in optional")
	}
}

func TestRemoveFGLDependencyFindsAnyScope(t *testing.T) {
	m := manifest.New("x", "1.0.0", "", "")
	m.AddFGLDependencyScoped("foo", "^1.0.0", manifest.ScopeDev)
	scope := m.RemoveFGLDependency("foo")
	if scope != manifest.ScopeDev {
		t.Errorf("expected ScopeDev, got %q", scope)
	}
	if _, ok := m.DevDependencies.FGL["foo"]; ok {
		t.Error("foo should be gone from dev")
	}
	// removing a non-existent name returns empty scope, no panic
	if got := m.RemoveFGLDependency("bar"); got != "" {
		t.Errorf("expected empty scope for absent name, got %q", got)
	}
}

func TestFindFGLDependencyReportsScope(t *testing.T) {
	m := manifest.New("x", "1.0.0", "", "")
	m.AddFGLDependencyScoped("a", "^1.0.0", manifest.ScopeProd)
	m.AddFGLDependencyScoped("b", "^1.0.0", manifest.ScopeOptional)
	if v, s := m.FindFGLDependency("a"); v != "^1.0.0" || s != manifest.ScopeProd {
		t.Errorf("a: got (%q,%q)", v, s)
	}
	if v, s := m.FindFGLDependency("b"); v != "^1.0.0" || s != manifest.ScopeOptional {
		t.Errorf("b: got (%q,%q)", v, s)
	}
	if v, s := m.FindFGLDependency("missing"); v != "" || s != "" {
		t.Errorf("missing: got (%q,%q)", v, s)
	}
}

// Omitempty on DevDependencies/OptionalDependencies keeps existing manifests
// free of cruft when those scopes are unused.
func TestOmitEmptyScopedDependencies(t *testing.T) {
	m := manifest.New("x", "1.0.0", "", "")
	m.AddFGLDependency("only-prod", "^1.0.0")
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)
	if containsHelper(out, "devDependencies") {
		t.Errorf("expected devDependencies omitted, got: %s", out)
	}
	if containsHelper(out, "optionalDependencies") {
		t.Errorf("expected optionalDependencies omitted, got: %s", out)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
