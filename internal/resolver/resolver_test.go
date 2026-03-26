package resolver_test

import (
	"errors"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// ─── Fake registry ────────────────────────────────────────────────────────────

// dbEntry is one version in the fake registry, with its Genero constraint.
type dbEntry struct {
	info             *registry.PackageInfo
	generoConstraint string // semver constraint on the Genero runtime
}

// packageDB maps package name → version string → dbEntry.
type packageDB map[string]map[string]dbEntry

func (db packageDB) versions(name string) ([]resolver.CandidateVersion, error) {
	pkg, ok := db[name]
	if !ok {
		return nil, errors.New("package not found: " + name)
	}
	out := make([]resolver.CandidateVersion, 0, len(pkg))
	for vs, entry := range pkg {
		out = append(out, resolver.CandidateVersion{
			Version:          semver.MustParse(vs),
			GeneroConstraint: entry.generoConstraint,
		})
	}
	return out, nil
}

func (db packageDB) info(name, version string) (*registry.PackageInfo, error) {
	pkg, ok := db[name]
	if !ok {
		return nil, errors.New("package not found: " + name)
	}
	entry, ok := pkg[version]
	if !ok {
		return nil, errors.New("version not found: " + name + "@" + version)
	}
	return entry.info, nil
}

func (db packageDB) newResolver(gv genero.Version) *resolver.Resolver {
	return resolver.NewWithFetchers(gv, db.versions, db.info)
}

// ─── Builder helpers ──────────────────────────────────────────────────────────

// entry builds a dbEntry. generoConstraint="" means "any version".
func entry(generoConstraint string, info *registry.PackageInfo) dbEntry {
	return dbEntry{generoConstraint: generoConstraint, info: info}
}

func pkg(name, version string, fglDeps map[string]string, javaDeps ...manifest.JavaDependency) *registry.PackageInfo {
	return &registry.PackageInfo{
		Name:        name,
		Version:     version,
		DownloadURL: "https://example.com/" + name + "-" + version + ".zip",
		Checksum:    "deadbeef",
		FGLDeps:     fglDeps,
		JavaDeps:    javaDeps,
	}
}

func jar(groupID, artifactID, version string) manifest.JavaDependency {
	return manifest.JavaDependency{GroupID: groupID, ArtifactID: artifactID, Version: version}
}

var (
	genero401 = genero.MustParse("4.01.12")
	genero320 = genero.MustParse("3.20.05")
)

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestNoDeps(t *testing.T) {
	db := packageDB{}
	plan, err := db.newResolver(genero401).Resolve(manifest.New("myapp", "1.0.0", "", ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Packages) != 0 {
		t.Errorf("expected 0 packages, got %d", len(plan.Packages))
	}
}

func TestDirectDeps(t *testing.T) {
	db := packageDB{
		"utils": {
			"1.0.0": entry("", pkg("utils", "1.0.0", nil)),
			"1.1.0": entry("", pkg("utils", "1.1.0", nil)),
			"1.2.0": entry("", pkg("utils", "1.2.0", nil)),
		},
		"dbtools": {
			"2.0.0": entry("", pkg("dbtools", "2.0.0", nil)),
			"2.1.0": entry("", pkg("dbtools", "2.1.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("utils", "^1.0.0")
	root.AddFGLDependency("dbtools", "^2.0.0")

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := planByName(plan)
	assertVersion(t, byName, "utils", "1.2.0")
	assertVersion(t, byName, "dbtools", "2.1.0")
}

func TestTransitiveDeps(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0", map[string]string{"b": "^1.0.0"}))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", map[string]string{"c": "^2.0.0"}))},
		"c": {
			"2.0.0": entry("", pkg("c", "2.0.0", nil)),
			"2.1.0": entry("", pkg("c", "2.1.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Packages) != 3 {
		t.Fatalf("expected 3 packages, got %d", len(plan.Packages))
	}
	byName := planByName(plan)
	assertVersion(t, byName, "a", "1.0.0")
	assertVersion(t, byName, "b", "1.0.0")
	assertVersion(t, byName, "c", "2.1.0")
}

func TestSharedDepCompatible(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0", map[string]string{"c": "^1.0.0"}))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", map[string]string{"c": ">=1.1.0"}))},
		"c": {
			"1.0.0": entry("", pkg("c", "1.0.0", nil)),
			"1.1.0": entry("", pkg("c", "1.1.0", nil)),
			"1.2.0": entry("", pkg("c", "1.2.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")
	root.AddFGLDependency("b", "^1.0.0")

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Packages) != 3 {
		t.Fatalf("expected 3 packages, got %d", len(plan.Packages))
	}
	assertVersion(t, planByName(plan), "c", "1.2.0")
}

func TestSharedDepConflict(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0", map[string]string{"c": "^1.0.0"}))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", map[string]string{"c": "^2.0.0"}))},
		"c": {
			"1.0.0": entry("", pkg("c", "1.0.0", nil)),
			"2.0.0": entry("", pkg("c", "2.0.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")
	root.AddFGLDependency("b", "^1.0.0")

	_, err := db.newResolver(genero401).Resolve(root)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	var cl *resolver.ConflictList
	if !errors.As(err, &cl) {
		t.Fatalf("expected *ConflictList, got %T: %v", err, err)
	}
	found := false
	for _, c := range cl.Conflicts {
		if c.Package == "c" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected conflict for 'c', got: %v", cl.Conflicts)
	}
}

// TestGeneroFilteringExcludesIncompatible: package has two versions; only the
// one compatible with the detected Genero version should be chosen.
func TestGeneroFilteringExcludesIncompatible(t *testing.T) {
	db := packageDB{
		"utils": {
			// 1.0.0 was compiled for Genero 3.x only
			"1.0.0": entry("^3.0.0", pkg("utils", "1.0.0", nil)),
			// 2.0.0 supports Genero 4.x
			"2.0.0": entry("^4.0.0", pkg("utils", "2.0.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("utils", "*")

	// Running Genero 4.01 — should pick 2.0.0, not 1.0.0
	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertVersion(t, planByName(plan), "utils", "2.0.0")
}

// TestGeneroFilteringPicksHighestCompatible: multiple versions compatible with
// the detected Genero; highest should win.
func TestGeneroFilteringPicksHighestCompatible(t *testing.T) {
	db := packageDB{
		"utils": {
			"1.0.0": entry(">=3.0.0", pkg("utils", "1.0.0", nil)),
			"1.1.0": entry(">=3.0.0", pkg("utils", "1.1.0", nil)),
			"1.2.0": entry(">=4.0.0", pkg("utils", "1.2.0", nil)), // only 4.x+
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("utils", "^1.0.0")

	// Genero 3.20: 1.2.0 is excluded, highest compatible is 1.1.0
	plan320, err := db.newResolver(genero320).Resolve(root)
	if err != nil {
		t.Fatalf("Genero 3.20: unexpected error: %v", err)
	}
	assertVersion(t, planByName(plan320), "utils", "1.1.0")

	// Genero 4.01: all versions pass, highest is 1.2.0
	plan401, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("Genero 4.01: unexpected error: %v", err)
	}
	assertVersion(t, planByName(plan401), "utils", "1.2.0")
}

// TestGeneroNoCompatibleVersion: no version of the package supports the
// installed Genero — should return a clear error, not a conflict.
func TestGeneroNoCompatibleVersion(t *testing.T) {
	db := packageDB{
		"legacy": {
			"1.0.0": entry("^3.0.0", pkg("legacy", "1.0.0", nil)),
			"1.1.0": entry("^3.0.0", pkg("legacy", "1.1.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("legacy", "^1.0.0")

	_, err := db.newResolver(genero401).Resolve(root)
	if err == nil {
		t.Fatal("expected error for Genero-incompatible package, got nil")
	}
}

// TestRootGeneroConstraintRejected: root manifest's own genero constraint
// doesn't match the detected runtime.
func TestRootGeneroConstraintRejected(t *testing.T) {
	db := packageDB{}
	root := manifest.New("myapp", "1.0.0", "", "")
	root.GeneroConstraint = "^3.0.0" // requires Genero 3.x

	_, err := db.newResolver(genero401).Resolve(root) // but we have 4.x
	if err == nil {
		t.Fatal("expected error for mismatched root genero constraint, got nil")
	}
}

func TestJARCollection(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0",
			map[string]string{"b": "^1.0.0"},
			jar("com.google.code.gson", "gson", "2.9.0")))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", nil,
			jar("com.google.code.gson", "gson", "2.10.1"),
			jar("org.apache.commons", "commons-lang3", "3.12.0")))},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")
	root.AddJavaDependency(jar("org.slf4j", "slf4j-api", "2.0.0"))

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	jarsByKey := make(map[string]manifest.JavaDependency)
	for _, j := range plan.JARs {
		jarsByKey[j.Key()] = j
	}

	gson := jarsByKey["com.google.code.gson:gson"]
	if gson.Version != "2.10.1" {
		t.Errorf("expected gson 2.10.1, got %s", gson.Version)
	}
	if _, ok := jarsByKey["org.apache.commons:commons-lang3"]; !ok {
		t.Error("expected commons-lang3")
	}
	if _, ok := jarsByKey["org.slf4j:slf4j-api"]; !ok {
		t.Error("expected slf4j-api")
	}
	if len(plan.JARs) != 3 {
		t.Errorf("expected 3 JARs, got %d", len(plan.JARs))
	}
}

func TestCycleSafety(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0", map[string]string{"b": "^1.0.0"}))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", map[string]string{"a": "^1.0.0"}))},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")

	done := make(chan struct{})
	go func() {
		db.newResolver(genero401).Resolve(root) //nolint:errcheck
		close(done)
	}()
	select {
	case <-done:
	case <-timeoutChan(2):
		t.Fatal("resolver did not terminate — possible infinite loop on cyclic graph")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func planByName(plan *resolver.Plan) map[string]resolver.ResolvedPackage {
	m := make(map[string]resolver.ResolvedPackage, len(plan.Packages))
	for _, p := range plan.Packages {
		m[p.Name] = p
	}
	return m
}

func assertVersion(t *testing.T, byName map[string]resolver.ResolvedPackage, name, want string) {
	t.Helper()
	p, ok := byName[name]
	if !ok {
		t.Errorf("package %q not found in plan", name)
		return
	}
	if p.Version.String() != want {
		t.Errorf("package %q: got %s, want %s", name, p.Version, want)
	}
}

func timeoutChan(seconds int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		n := seconds * 1_000_000_000
		for i := 0; i < n; i++ {
		}
		close(ch)
	}()
	return ch
}
