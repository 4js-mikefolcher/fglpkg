package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// toolVersionLine is the single source of truth for the tool-version banner,
// shared by `fglpkg version`, `fglpkg --version`, and `fglpkg -v`.
func toolVersionLine() string {
	return fmt.Sprintf("fglpkg version %s (build %s)", Version, Build)
}

// cmdVersion handles the `version` command: it prints the fglpkg tool version
// and nothing else. It never touches fglpkg.json. Bumping the package version
// moved to `fglpkg bump` (GIS-288); passing a bump kind here is rejected with a
// redirect rather than silently mutating the manifest.
func cmdVersion(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf(
			"fglpkg version only prints the tool version and takes no arguments\n" +
				"to change the package version, use: fglpkg bump <patch|minor|major|prerelease|<semver>> [--git]",
		)
	}
	fmt.Println(toolVersionLine())
	return nil
}

// cmdBump dispatches the `bump` command. It rewrites the current project's
// fglpkg.json to the next version, and with --git also stages, commits, and
// tags the release. This is the mutating half of the old `version` command
// (GIS-288).
//
//	fglpkg bump patch                  → 1.2.3 → 1.2.4
//	fglpkg bump minor                  → 1.2.3 → 1.3.0
//	fglpkg bump major                  → 1.2.3 → 2.0.0
//	fglpkg bump prerelease             → 1.2.3 → 1.2.4-0, or bump numeric suffix
//	fglpkg bump 2.0.0-rc.1             → set to explicit semver
//	fglpkg bump <kind> --git           → also stage, commit, and tag
func cmdBump(args []string) error {
	var gitMode bool
	var bumpArg string
	for _, a := range args {
		switch a {
		case "--git":
			gitMode = true
		default:
			if bumpArg != "" {
				return fmt.Errorf("unexpected argument %q", a)
			}
			bumpArg = a
		}
	}
	if bumpArg == "" {
		return fmt.Errorf("usage: fglpkg bump <patch|minor|major|prerelease|<semver>> [--git]")
	}

	m, err := manifest.Load(".")
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no %s in current directory — run 'fglpkg init' first", manifest.Filename)
		}
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}

	current, err := semver.Parse(m.Version)
	if err != nil {
		return fmt.Errorf("current version %q is not valid semver: %w", m.Version, err)
	}

	next, err := bumpVersion(current, bumpArg)
	if err != nil {
		return err
	}
	if next.String() == current.String() {
		return fmt.Errorf("new version %s is the same as current — nothing to do", next)
	}

	if gitMode {
		if err := requireCleanGitTree(); err != nil {
			return err
		}
	}

	oldStr := m.Version
	m.Version = next.String()
	if err := m.Save("."); err != nil {
		return fmt.Errorf("failed to write %s: %w", manifest.Filename, err)
	}
	fmt.Printf("%s → %s (in %s)\n", oldStr, m.Version, manifest.Filename)

	// Keep the lockfile's recorded root version in step with the manifest — the
	// way npm's `npm version` also updates package-lock.json. Only when a lock
	// already exists: `bump` never resolves, so it must not create one. Bumping
	// the version changes no constraint, so the resolved graph and the
	// root.declared snapshot stay byte-for-byte identical; this is a field-only
	// edit, not a re-resolution (GIS-492).
	lockUpdated, err := syncLockVersion(".", next.String())
	if err != nil {
		return err
	}
	if lockUpdated {
		fmt.Printf("Updated %s (root version → %s)\n", lockfile.Filename, next.String())
	}

	if gitMode {
		tag := "v" + m.Version
		if err := runGit("add", manifest.Filename); err != nil {
			return err
		}
		// Stage the lock in the same commit so the tag points at a consistent
		// tree; without this the bump commit would leave the lock modified.
		if lockUpdated {
			if err := runGit("add", lockfile.Filename); err != nil {
				return err
			}
		}
		if err := runGit("commit", "-m", tag); err != nil {
			return err
		}
		if err := runGit("tag", tag); err != nil {
			return err
		}
		fmt.Printf("Created commit and tag %s\n", tag)
		fmt.Printf("To publish: git push && git push origin %s\n", tag)
	} else {
		fmt.Printf("To tag this release: git tag v%s\n", m.Version)
	}
	return nil
}

// syncLockVersion updates the root version recorded in dir's lockfile to match a
// freshly bumped manifest, returning whether it wrote anything. It is a no-op
// (false) when no lockfile exists — `fglpkg bump` never resolves, so it must not
// create a lock — and when the lock already records the target version. Only
// root.version changes: a version bump alters no dependency constraint, so the
// resolved entries and the root.declared snapshot are left untouched and the diff
// is a single line (GIS-492).
func syncLockVersion(dir, version string) (bool, error) {
	if !lockfile.Exists(dir) {
		return false, nil
	}
	lf, err := lockfile.Load(dir)
	if err != nil {
		return false, fmt.Errorf("failed to load %s: %w", lockfile.Filename, err)
	}
	if lf.RootManifest.Version == version {
		return false, nil
	}
	lf.RootManifest.Version = version
	if err := lf.Save(dir); err != nil {
		return false, fmt.Errorf("failed to write %s: %w", lockfile.Filename, err)
	}
	return true, nil
}

// bumpVersion returns the next Version for a given bump kind.
// kind is one of "patch", "minor", "major", "prerelease", or an explicit
// semver string to set directly.
func bumpVersion(cur semver.Version, kind string) (semver.Version, error) {
	switch kind {
	case "patch":
		return semver.Version{Major: cur.Major, Minor: cur.Minor, Patch: cur.Patch + 1}, nil
	case "minor":
		return semver.Version{Major: cur.Major, Minor: cur.Minor + 1, Patch: 0}, nil
	case "major":
		return semver.Version{Major: cur.Major + 1, Minor: 0, Patch: 0}, nil
	case "prerelease":
		return bumpPrerelease(cur), nil
	default:
		v, err := semver.Parse(kind)
		if err != nil {
			return semver.Version{}, fmt.Errorf(
				"unknown bump kind %q: expected patch|minor|major|prerelease or a valid semver",
				kind,
			)
		}
		return semver.Version{
			Major:      v.Major,
			Minor:      v.Minor,
			Patch:      v.Patch,
			PreRelease: v.PreRelease,
		}, nil
	}
}

// bumpPrerelease implements npm's `prerelease` semantics:
//
//   - 1.2.3              → 1.2.4-0    (start a prerelease of the next patch)
//   - 1.2.4-0            → 1.2.4-1    (increment numeric tail)
//   - 1.2.4-alpha.0      → 1.2.4-alpha.1
//   - 1.2.4-alpha        → 1.2.4-alpha.0
func bumpPrerelease(cur semver.Version) semver.Version {
	if cur.PreRelease == "" {
		return semver.Version{
			Major:      cur.Major,
			Minor:      cur.Minor,
			Patch:      cur.Patch + 1,
			PreRelease: "0",
		}
	}
	idx := strings.LastIndex(cur.PreRelease, ".")
	prefix := ""
	tail := cur.PreRelease
	if idx >= 0 {
		prefix = cur.PreRelease[:idx+1]
		tail = cur.PreRelease[idx+1:]
	}
	if n, err := strconv.Atoi(tail); err == nil {
		return semver.Version{
			Major:      cur.Major,
			Minor:      cur.Minor,
			Patch:      cur.Patch,
			PreRelease: prefix + strconv.Itoa(n+1),
		}
	}
	return semver.Version{
		Major:      cur.Major,
		Minor:      cur.Minor,
		Patch:      cur.Patch,
		PreRelease: cur.PreRelease + ".0",
	}
}

// requireCleanGitTree returns an error if the working tree has uncommitted
// changes. Used as a pre-flight for --git mode so the commit we create is
// not mixed with unrelated work.
func requireCleanGitTree() error {
	cmd := exec.Command("git", "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("cannot run 'git status' (is this a git repo?): %w", err)
	}
	if len(bytes.TrimSpace(out)) > 0 {
		return fmt.Errorf(
			"git working tree is not clean; commit or stash changes before using --git\n\n%s",
			out,
		)
	}
	return nil
}

func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
