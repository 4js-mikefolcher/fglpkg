// Package resolver implements transitive dependency resolution for fglpkg.
//
// Resolution algorithm:
//  1. Detect the installed Genero BDL version (or accept an override).
//  2. Start with the root manifest's direct dependencies as the work queue.
//  3. For each package, fetch its available versions from the registry.
//     Filter to those whose generoConstraint is satisfied by the detected
//     Genero version, then apply the semver package constraint.
//  4. If the package has already been seen, intersect the new constraint with
//     accumulated constraints — if no version satisfies all, report a conflict.
//  5. Recurse into each resolved package's own dependencies (BFS).
//  6. Return a flat, ordered install plan with no duplicates.
//
// Java JAR dependencies are collected separately and deduplicated; when the
// same JAR appears at different versions the higher version wins.
package resolver

import (
	"fmt"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// ResolvedPackage is a single BDL package in the final install plan.
type ResolvedPackage struct {
	Name        string
	Version     semver.Version
	DownloadURL string
	Checksum    string
	// RequiredBy lists the packages that introduced this dependency.
	RequiredBy []string
}

// Plan is the complete, ordered install plan produced by resolution.
type Plan struct {
	Packages     []ResolvedPackage
	JARs         []manifest.JavaDependency
	GeneroVersion genero.Version // the runtime version used during resolution
}

// Conflict describes a version conflict between two or more requirers.
type Conflict struct {
	Package     string
	Constraints []constraintSource
}

func (c Conflict) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "version conflict for %q:\n", c.Package)
	for _, cs := range c.Constraints {
		fmt.Fprintf(&b, "  %s requires %q\n", cs.requiredBy, cs.constraint)
	}
	return b.String()
}

type constraintSource struct {
	constraint string
	requiredBy string
}

// CandidateVersion pairs a parsed semver version with its Genero constraint.
// Exported so test packages can construct fake VersionFetcher responses.
type CandidateVersion struct {
	Version          semver.Version
	GeneroConstraint string
}

// VersionFetcher fetches available versions and their Genero constraints.
type VersionFetcher func(name string) ([]CandidateVersion, error)

// InfoFetcher fetches full package metadata for a resolved name@version.
type InfoFetcher func(name, version string) (*registry.PackageInfo, error)

// Resolver resolves the full transitive dependency graph.
type Resolver struct {
	fetchVersions VersionFetcher
	fetchInfo     InfoFetcher
	generoVersion genero.Version
	ws            *workspace.Workspace // nil when not in a workspace
}

// New creates a Resolver that auto-detects the Genero version, detects any
// workspace from the current directory, and uses the live registry.
func New() (*Resolver, error) {
	gv, err := genero.Detect()
	if err != nil {
		return nil, fmt.Errorf("cannot create resolver: %w", err)
	}
	r := &Resolver{
		fetchVersions: registryVersions,
		fetchInfo:     registryInfo,
		generoVersion: gv,
	}
	// Auto-detect workspace — nil if not in one.
	if wsRoot := workspace.FindRoot("."); wsRoot != "" {
		ws, err := workspace.Load(wsRoot)
		if err != nil {
			return nil, fmt.Errorf("cannot load workspace: %w", err)
		}
		r.ws = ws
	}
	return r, nil
}

// NewWithFetchers creates a Resolver with injectable fetchers and a fixed
// Genero version (for testing). ws may be nil.
func NewWithFetchers(gv genero.Version, fv VersionFetcher, fi InfoFetcher) *Resolver {
	return &Resolver{fetchVersions: fv, fetchInfo: fi, generoVersion: gv}
}

// WithWorkspace attaches a workspace to an existing Resolver.
func (r *Resolver) WithWorkspace(ws *workspace.Workspace) *Resolver {
	r.ws = ws
	return r
}

// Resolve resolves all transitive dependencies of the given root manifest.
// Returns a Plan or an error (which may be a *ConflictList).
func (r *Resolver) Resolve(root *manifest.Manifest) (*Plan, error) {
	if ok, err := r.generoVersion.Satisfies(root.GeneroConstraint); err != nil {
		return nil, fmt.Errorf("invalid genero constraint in root manifest: %w", err)
	} else if !ok {
		return nil, fmt.Errorf(
			"project requires Genero %q but detected version is %s",
			root.GeneroConstraint, r.generoVersion,
		)
	}

	state := newState()

	for name, constraint := range root.Dependencies.FGL {
		// If this dep is a local workspace member, record it and skip registry.
		if r.ws != nil && r.ws.IsLocal(name) {
			member := r.ws.Member(name)
			state.addLocalMember(LocalMember{
				Name:    member.Manifest.Name,
				Version: member.Manifest.Version,
				Path:    member.Path,
			})
			// Still enqueue its transitive deps for resolution.
			for depName, depConstraint := range member.Manifest.Dependencies.FGL {
				if r.ws.IsLocal(depName) {
					localDep := r.ws.Member(depName)
					state.addLocalMember(LocalMember{
						Name:    localDep.Manifest.Name,
						Version: localDep.Manifest.Version,
						Path:    localDep.Path,
					})
				} else {
					state.enqueue(workItem{name: depName, constraint: depConstraint, requiredBy: name})
				}
			}
			for _, jar := range member.Manifest.Dependencies.Java {
				state.addJAR(jar)
			}
			continue
		}
		state.enqueue(workItem{name: name, constraint: constraint, requiredBy: "<root>"})
	}
	for _, dep := range root.Dependencies.Java {
		state.addJAR(dep)
	}

	for state.hasWork() {
		item := state.dequeue()

		// Skip local workspace members encountered transitively.
		if r.ws != nil && r.ws.IsLocal(item.name) {
			member := r.ws.Member(item.name)
			state.addLocalMember(LocalMember{
				Name:    member.Manifest.Name,
				Version: member.Manifest.Version,
				Path:    member.Path,
			})
			continue
		}

		if state.isResolved(item.name) {
			if err := state.addConstraint(item.name, item.constraint, item.requiredBy); err != nil {
				state.addConflict(err.(Conflict))
			}
			if err := state.checkExistingResolution(item.name, item.constraint, item.requiredBy); err != nil {
				state.addConflict(err.(Conflict))
			}
			continue
		}

		state.addConstraint(item.name, item.constraint, item.requiredBy) //nolint:errcheck

		candidates, err := r.fetchVersions(item.name)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch versions for %q: %w", item.name, err)
		}

		generoCompatible, err := r.filterByGenero(item.name, candidates)
		if err != nil {
			return nil, err
		}
		if len(generoCompatible) == 0 {
			return nil, fmt.Errorf(
				"no version of %q is compatible with Genero %s",
				item.name, r.generoVersion,
			)
		}

		chosen, err := state.bestVersion(item.name, generoCompatible)
		if err != nil {
			state.addConflict(Conflict{
				Package:     item.name,
				Constraints: state.constraints[item.name],
			})
			continue
		}

		info, err := r.fetchInfo(item.name, chosen.String())
		if err != nil {
			return nil, fmt.Errorf("failed to fetch info for %s@%s: %w", item.name, chosen, err)
		}

		state.markResolved(item.name, chosen, info)

		for depName, depConstraint := range info.FGLDeps {
			if r.ws != nil && r.ws.IsLocal(depName) {
				member := r.ws.Member(depName)
				state.addLocalMember(LocalMember{
					Name:    member.Manifest.Name,
					Version: member.Manifest.Version,
					Path:    member.Path,
				})
				continue
			}
			if state.isResolved(depName) {
				if err := state.checkExistingResolution(depName, depConstraint, item.name); err != nil {
					state.addConflict(err.(Conflict))
				}
				continue
			}
			state.enqueue(workItem{name: depName, constraint: depConstraint, requiredBy: item.name})
		}
		for _, jar := range info.JavaDeps {
			state.addJAR(jar)
		}
	}

	if len(state.conflicts) > 0 {
		return nil, &ConflictList{Conflicts: state.conflicts}
	}

	plan := state.buildPlan()
	plan.GeneroVersion = r.generoVersion
	return plan, nil
}

// filterByGenero removes candidate versions whose generoConstraint is not
// satisfied by the detected Genero runtime version.
func (r *Resolver) filterByGenero(pkgName string, candidates []CandidateVersion) ([]semver.Version, error) {
	out := make([]semver.Version, 0, len(candidates))
	for _, c := range candidates {
		ok, err := r.generoVersion.Satisfies(c.GeneroConstraint)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s@%s has invalid genero constraint %q: %v — skipping\n",
				pkgName, c.Version, c.GeneroConstraint, err)
			continue
		}
		if ok {
			out = append(out, c.Version)
		}
	}
	return out, nil
}

// ─── ConflictList ─────────────────────────────────────────────────────────────

// ConflictList is returned when one or more version conflicts are detected.
type ConflictList struct {
	Conflicts []Conflict
}

func (cl *ConflictList) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d dependency conflict(s) found:\n\n", len(cl.Conflicts))
	for _, c := range cl.Conflicts {
		b.WriteString(c.Error())
		b.WriteString("\n")
	}
	return b.String()
}

// ─── Resolution state ─────────────────────────────────────────────────────────

type workItem struct {
	name       string
	constraint string
	requiredBy string
}

type resolvedEntry struct {
	version semver.Version
	info    *registry.PackageInfo
	order   int
}

type state struct {
	queue        []workItem
	constraints  map[string][]constraintSource
	resolved     map[string]*resolvedEntry
	jars         map[string]manifest.JavaDependency
	localMembers map[string]LocalMember // name → local workspace member
	conflicts    []Conflict
	orderSeq     int
}

func newState() *state {
	return &state{
		constraints:  make(map[string][]constraintSource),
		resolved:     make(map[string]*resolvedEntry),
		jars:         make(map[string]manifest.JavaDependency),
		localMembers: make(map[string]LocalMember),
	}
}

func (s *state) addLocalMember(lm LocalMember) { s.localMembers[lm.Name] = lm }

func (s *state) enqueue(item workItem)  { s.queue = append(s.queue, item) }
func (s *state) dequeue() workItem      { item := s.queue[0]; s.queue = s.queue[1:]; return item }
func (s *state) hasWork() bool          { return len(s.queue) > 0 }
func (s *state) isResolved(n string) bool { _, ok := s.resolved[n]; return ok }

func (s *state) addConstraint(name, constraint, requiredBy string) error {
	s.constraints[name] = append(s.constraints[name], constraintSource{
		constraint: constraint,
		requiredBy: requiredBy,
	})
	return nil
}

func (s *state) bestVersion(name string, candidates []semver.Version) (semver.Version, error) {
	parsed := make([]semver.Constraint, 0, len(s.constraints[name]))
	for _, cs := range s.constraints[name] {
		c, err := semver.ParseConstraint(cs.constraint)
		if err != nil {
			return semver.Version{}, fmt.Errorf("invalid constraint %q from %s: %w",
				cs.constraint, cs.requiredBy, err)
		}
		parsed = append(parsed, c)
	}

	var best *semver.Version
	for _, v := range candidates {
		v := v
		ok := true
		for _, c := range parsed {
			if !c.Matches(v) {
				ok = false
				break
			}
		}
		if ok && (best == nil || v.GreaterThan(*best)) {
			best = &v
		}
	}

	if best == nil {
		return semver.Version{}, fmt.Errorf("no version satisfies all constraints")
	}
	return *best, nil
}

func (s *state) checkExistingResolution(name, newConstraint, requiredBy string) error {
	entry := s.resolved[name]
	c, err := semver.ParseConstraint(newConstraint)
	if err != nil {
		return nil
	}
	s.constraints[name] = append(s.constraints[name], constraintSource{
		constraint: newConstraint,
		requiredBy: requiredBy,
	})
	if !c.Matches(entry.version) {
		return Conflict{Package: name, Constraints: s.constraints[name]}
	}
	return nil
}

func (s *state) markResolved(name string, v semver.Version, info *registry.PackageInfo) {
	s.resolved[name] = &resolvedEntry{version: v, info: info, order: s.orderSeq}
	s.orderSeq++
}

func (s *state) addConflict(c Conflict) { s.conflicts = append(s.conflicts, c) }

func (s *state) addJAR(dep manifest.JavaDependency) {
	key := dep.Key()
	if existing, ok := s.jars[key]; ok {
		ev, _ := semver.Parse(existing.Version)
		nv, _ := semver.Parse(dep.Version)
		if nv.GreaterThan(ev) {
			s.jars[key] = dep
		}
	} else {
		s.jars[key] = dep
	}
}

func (s *state) buildPlan() *Plan {
	pkgs := make([]ResolvedPackage, 0, len(s.resolved))
	for name, entry := range s.resolved {
		var requiredBy []string
		for _, cs := range s.constraints[name] {
			requiredBy = append(requiredBy, cs.requiredBy)
		}
		pkgs = append(pkgs, ResolvedPackage{
			Name:        name,
			Version:     entry.version,
			DownloadURL: entry.info.DownloadURL,
			Checksum:    entry.info.Checksum,
			RequiredBy:  requiredBy,
		})
	}
	for i := 1; i < len(pkgs); i++ {
		for j := i; j > 0 && s.resolved[pkgs[j].Name].order < s.resolved[pkgs[j-1].Name].order; j-- {
			pkgs[j], pkgs[j-1] = pkgs[j-1], pkgs[j]
		}
	}

	jars := make([]manifest.JavaDependency, 0, len(s.jars))
	for _, dep := range s.jars {
		jars = append(jars, dep)
	}

	locals := make([]LocalMember, 0, len(s.localMembers))
	for _, lm := range s.localMembers {
		locals = append(locals, lm)
	}

	return &Plan{Packages: pkgs, JARs: jars, LocalMembers: locals}
}

// ─── Live registry fetchers ───────────────────────────────────────────────────

func registryVersions(name string) ([]candidateVersion, error) {
	vl, err := registry.FetchVersionList(name)
	if err != nil {
		return nil, err
	}
	out := make([]candidateVersion, 0, len(vl.Versions))
	for _, ve := range vl.VersionEntries {
		v, err := semver.Parse(ve.Version)
		if err != nil {
			continue
		}
		out = append(out, candidateVersion{
			version:          v,
			generoConstraint: ve.GeneroConstraint,
		})
	}
	return out, nil
}

func registryInfo(name, version string) (*registry.PackageInfo, error) {
	return registry.FetchInfo(name, version)
}

// errWriter returns stderr; extracted so tests can redirect if needed.
func errWriter() *os.File { return os.Stderr }
