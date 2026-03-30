// Package genero detects the installed Genero BDL runtime version and
// provides compatibility checking against generoConstraint expressions.
//
// Detection strategy (tried in order):
//  1. FGLPKG_GENERO_VERSION env var  — explicit override, useful in CI
//  2. `fglcomp --version`            — most reliable when fglcomp is on PATH
//  3. $FGLDIR/etc/fgl.version        — fallback file present in most installs
//  4. $FGLDIR/bin/fglcomp --version  — fallback when fglcomp not on PATH
package genero

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// Version represents an installed Genero BDL runtime version.
type Version struct {
	sv       semver.Version
	original string // e.g. "4.01.12"
}

// String returns the version as originally detected (e.g. "4.01.12").
func (v Version) String() string { return v.original }

// Semver returns the version as a semver.Version for constraint matching.
func (v Version) Semver() semver.Version { return v.sv }

// MajorString returns the Genero major version as a string (e.g. "4").
// This is used as the variant key for platform-specific package builds.
func (v Version) MajorString() string { return fmt.Sprintf("%d", v.sv.Major) }

// Detect attempts to determine the installed Genero BDL version using
// the strategy described in the package doc.
func Detect() (Version, error) {
	// 1. Explicit override.
	if s := os.Getenv("FGLPKG_GENERO_VERSION"); s != "" {
		return parse(s)
	}

	// 2. fglcomp on PATH.
	if v, err := fromCommand("fglcomp", "--version"); err == nil {
		return v, nil
	}

	// 3. $FGLDIR/etc/fgl.version file.
	if fgldir := os.Getenv("FGLDIR"); fgldir != "" {
		if v, err := fromVersionFile(filepath.Join(fgldir, "etc", "fgl.version")); err == nil {
			return v, nil
		}

		// 4. $FGLDIR/bin/fglcomp --version.
		if v, err := fromCommand(filepath.Join(fgldir, "bin", "fglcomp"), "--version"); err == nil {
			return v, nil
		}
	}

	return Version{}, fmt.Errorf(
		"cannot detect Genero BDL version: fglcomp not found on PATH and $FGLDIR is not set.\n" +
			"Set FGLPKG_GENERO_VERSION (e.g. FGLPKG_GENERO_VERSION=4.01.12) to override",
	)
}

// MustDetect calls Detect and panics on error. Useful in tests with a known env.
func MustDetect() Version {
	v, err := Detect()
	if err != nil {
		panic(err)
	}
	return v
}

// Parse parses a Genero version string such as "4.01.12" or "3.20.5".
// Genero uses MAJOR.MINOR.PATCH where MINOR may have leading zeros (e.g. "01").
func Parse(s string) (Version, error) { return parse(s) }

// MustParse parses and panics on error.
func MustParse(s string) Version {
	v, err := parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Satisfies reports whether this Genero version satisfies the given constraint
// string (uses the same semver constraint syntax as package versions).
// An empty constraint is treated as "any version".
func (v Version) Satisfies(constraint string) (bool, error) {
	if constraint == "" || constraint == "*" {
		return true, nil
	}
	c, err := semver.ParseConstraint(constraint)
	if err != nil {
		return false, fmt.Errorf("invalid genero constraint %q: %w", constraint, err)
	}
	return c.Matches(v.sv), nil
}

// ─── Detection helpers ────────────────────────────────────────────────────────

// versionPattern matches Genero version strings embedded in command output.
// Handles formats like:
//   "Genero BDL Version 4.01.12 ..."
//   "fglcomp: Genero BDL 3.20.05-..."
//   "4.01.12"
var versionPattern = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

func fromCommand(name string, args ...string) (Version, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		// Some versions exit non-zero for --version; still try to parse output.
		if len(out) == 0 {
			return Version{}, fmt.Errorf("command %q failed: %w", name, err)
		}
	}
	return extractVersion(string(out))
}

func fromVersionFile(path string) (Version, error) {
	f, err := os.Open(path)
	if err != nil {
		return Version{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if v, err := extractVersion(line); err == nil {
			return v, nil
		}
	}
	return Version{}, fmt.Errorf("no version found in %s", path)
}

func extractVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("no version pattern found in %q", s)
	}
	return parse(m[0])
}

// parse converts a "MAJOR.MINOR.PATCH" string into a Version.
// Leading zeros in MINOR/PATCH are accepted (e.g. "4.01.05").
func parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	// Strip leading zeros in each component before passing to semver.Parse,
	// since semver rejects octal-looking numbers.
	normalised := normaliseVersionString(s)
	sv, err := semver.Parse(normalised)
	if err != nil {
		return Version{}, fmt.Errorf("invalid Genero version %q: %w", s, err)
	}
	return Version{sv: sv, original: s}, nil
}

// normaliseVersionString strips leading zeros from each dot-separated component.
func normaliseVersionString(s string) string {
	parts := strings.Split(s, ".")
	for i, p := range parts {
		// Trim leading zeros but keep at least one digit.
		trimmed := strings.TrimLeft(p, "0")
		if trimmed == "" {
			trimmed = "0"
		}
		parts[i] = trimmed
	}
	return strings.Join(parts, ".")
}
