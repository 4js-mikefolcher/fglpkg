// Package semver implements semantic versioning parsing, comparison, and
// constraint matching for fglpkg.
//
// Supported version format:
//
//	MAJOR.MINOR.PATCH[-prerelease][+build]
//
// Supported constraint operators:
//
//	1.2.3        exact match
//	=1.2.3       exact match (explicit)
//	>1.2.3       greater than
//	>=1.2.3      greater than or equal
//	<1.2.3       less than
//	<=1.2.3      less than or equal
//	~1.2.3       patch-level range: >=1.2.3 <1.3.0
//	~1.2         minor-level range: >=1.2.0 <1.3.0
//	~1           major-level range: >=1.0.0 <2.0.0
//	^1.2.3       compatible range:  >=1.2.3 <2.0.0
//	^0.2.3       compatible range:  >=0.2.3 <0.3.0 (major=0 special case)
//	^0.0.3       compatible range:  >=0.0.3 <0.0.4 (major=0,minor=0 special case)
//	*  or latest any version
//	1.2.x        wildcard patch:    >=1.2.0 <1.3.0
//	1.x          wildcard minor:    >=1.0.0 <2.0.0
//
// Compound constraints (AND) are space-separated:   >=1.2.0 <2.0.0
// Compound constraints (OR)  are pipe-separated:    ^1.2.0 || ^2.0.0
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version represents a parsed semantic version.
type Version struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	PreRelease string // e.g. "alpha.1", "beta", "rc.2"
	Build      string // build metadata (ignored in comparisons)
	original   string
}

// String returns the original version string, or a reconstructed one.
func (v Version) String() string {
	if v.original != "" {
		return v.original
	}
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.PreRelease != "" {
		s += "-" + v.PreRelease
	}
	return s
}

// Parse parses a version string. Leading "v" is stripped.
func Parse(s string) (Version, error) {
	orig := s
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")

	// Strip build metadata
	build := ""
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		build = s[idx+1:]
		s = s[:idx]
	}

	// Split prerelease
	pre := ""
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q: expected MAJOR.MINOR.PATCH", orig)
	}

	major, err := parseUint(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid major in %q: %w", orig, err)
	}
	minor, err := parseUint(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid minor in %q: %w", orig, err)
	}
	patch, err := parseUint(parts[2])
	if err != nil {
		return Version{}, fmt.Errorf("invalid patch in %q: %w", orig, err)
	}

	return Version{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		PreRelease: pre,
		Build:      build,
		original:   orig,
	}, nil
}

// MustParse parses a version and panics on error. Useful in tests and constants.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Compare returns -1, 0, or 1 if v is less than, equal to, or greater than other.
// Build metadata is ignored. Pre-release versions have lower precedence than releases.
func (v Version) Compare(other Version) int {
	if c := cmpUint(v.Major, other.Major); c != 0 {
		return c
	}
	if c := cmpUint(v.Minor, other.Minor); c != 0 {
		return c
	}
	if c := cmpUint(v.Patch, other.Patch); c != 0 {
		return c
	}
	return cmpPreRelease(v.PreRelease, other.PreRelease)
}

func (v Version) Equal(other Version) bool      { return v.Compare(other) == 0 }
func (v Version) LessThan(other Version) bool   { return v.Compare(other) < 0 }
func (v Version) GreaterThan(other Version) bool { return v.Compare(other) > 0 }

// ─── Constraint ──────────────────────────────────────────────────────────────

// Constraint represents a parsed version constraint expression.
// Internally it is a list of OR groups, each group being AND-ed predicates.
type Constraint struct {
	raw    string
	groups []andGroup // OR of groups
}

type andGroup []predicate

type predicate struct {
	op  string  // "=", ">", ">=", "<", "<="
	ver Version
}

func (p predicate) matches(v Version) bool {
	c := v.Compare(p.ver)
	switch p.op {
	case "=":
		return c == 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	}
	return false
}

// ParseConstraint parses a constraint expression such as "^1.2.3" or ">=1.0 <2.0".
func ParseConstraint(s string) (Constraint, error) {
	raw := s
	s = strings.TrimSpace(s)

	if s == "" || s == "*" || strings.ToLower(s) == "latest" {
		return Constraint{raw: raw, groups: []andGroup{{}}}, nil // matches any
	}

	// Split OR groups on "||"
	orParts := strings.Split(s, "||")
	groups := make([]andGroup, 0, len(orParts))

	for _, part := range orParts {
		part = strings.TrimSpace(part)
		preds, err := parseAndGroup(part)
		if err != nil {
			return Constraint{}, fmt.Errorf("invalid constraint %q: %w", raw, err)
		}
		groups = append(groups, preds)
	}

	return Constraint{raw: raw, groups: groups}, nil
}

// MustParseConstraint parses a constraint and panics on error.
func MustParseConstraint(s string) Constraint {
	c, err := ParseConstraint(s)
	if err != nil {
		panic(err)
	}
	return c
}

// String returns the original constraint string.
func (c Constraint) String() string { return c.raw }

// Matches reports whether version v satisfies the constraint.
func (c Constraint) Matches(v Version) bool {
	// Skip pre-release versions unless the constraint explicitly includes one
	if v.PreRelease != "" && !c.allowsPreRelease() {
		return false
	}

	for _, group := range c.groups {
		if groupMatches(group, v) {
			return true
		}
	}
	return false
}

func groupMatches(g andGroup, v Version) bool {
	for _, p := range g {
		if !p.matches(v) {
			return false
		}
	}
	return true
}

func (c Constraint) allowsPreRelease() bool {
	for _, g := range c.groups {
		for _, p := range g {
			if p.ver.PreRelease != "" {
				return true
			}
		}
	}
	return false
}

// ─── Latest ──────────────────────────────────────────────────────────────────

// Latest returns the highest version from candidates that satisfies the constraint.
// Returns an error if no candidate matches.
func (c Constraint) Latest(candidates []Version) (Version, error) {
	var best *Version
	for i := range candidates {
		v := candidates[i]
		if c.Matches(v) {
			if best == nil || v.GreaterThan(*best) {
				best = &candidates[i]
			}
		}
	}
	if best == nil {
		return Version{}, fmt.Errorf("no version satisfies constraint %q", c.raw)
	}
	return *best, nil
}

// ─── Constraint parsing helpers ──────────────────────────────────────────────

// parseAndGroup parses a space-separated list of constraint tokens into predicates.
func parseAndGroup(s string) (andGroup, error) {
	tokens := strings.Fields(s)
	var preds andGroup

	for _, tok := range tokens {
		ps, err := parseToken(tok)
		if err != nil {
			return nil, err
		}
		preds = append(preds, ps...)
	}

	if len(preds) == 0 {
		// Empty group = match-all
		return andGroup{}, nil
	}
	return preds, nil
}

// parseToken converts a single constraint token into one or more predicates.
func parseToken(tok string) ([]predicate, error) {
	tok = strings.TrimSpace(tok)

	// Wildcard
	if tok == "*" || strings.ToLower(tok) == "latest" {
		return []predicate{}, nil
	}

	// Tilde ~ operator
	if strings.HasPrefix(tok, "~") {
		return parseTilde(strings.TrimPrefix(tok, "~"))
	}

	// Caret ^ operator
	if strings.HasPrefix(tok, "^") {
		return parseCaret(strings.TrimPrefix(tok, "^"))
	}

	// Comparison operators
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(tok, op) {
			vstr := strings.TrimPrefix(tok, op)
			v, err := parsePartial(vstr)
			if err != nil {
				return nil, fmt.Errorf("invalid version in %q: %w", tok, err)
			}
			return []predicate{{op: op, ver: v}}, nil
		}
	}

	// x-range: 1.2.x, 1.x, x
	if strings.ContainsAny(tok, "xX*") {
		return parseXRange(tok)
	}

	// Bare version → exact match (but allow partial like "1.2" → "1.2.0")
	v, err := parsePartial(tok)
	if err != nil {
		return nil, fmt.Errorf("invalid version %q: %w", tok, err)
	}
	return []predicate{{op: "=", ver: v}}, nil
}

// parseTilde handles ~MAJOR.MINOR.PATCH, ~MAJOR.MINOR, ~MAJOR
func parseTilde(s string) ([]predicate, error) {
	parts := strings.Split(s, ".")
	switch len(parts) {
	case 1: // ~1 → >=1.0.0 <2.0.0
		maj, err := parseUint(parts[0])
		if err != nil {
			return nil, err
		}
		return rangePreds(maj, 0, 0, maj+1, 0, 0), nil

	case 2: // ~1.2 → >=1.2.0 <1.3.0
		maj, err := parseUint(parts[0])
		if err != nil {
			return nil, err
		}
		min, err := parseUint(parts[1])
		if err != nil {
			return nil, err
		}
		return rangePreds(maj, min, 0, maj, min+1, 0), nil

	case 3: // ~1.2.3 → >=1.2.3 <1.3.0
		maj, err := parseUint(parts[0])
		if err != nil {
			return nil, err
		}
		min, err := parseUint(parts[1])
		if err != nil {
			return nil, err
		}
		patch, err := parseUint(parts[2])
		if err != nil {
			return nil, err
		}
		return rangePreds(maj, min, patch, maj, min+1, 0), nil
	}
	return nil, fmt.Errorf("invalid tilde range: ~%s", s)
}

// parseCaret handles ^MAJOR.MINOR.PATCH with npm-compatible semantics.
func parseCaret(s string) ([]predicate, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid caret range: ^%s (expected MAJOR.MINOR.PATCH)", s)
	}

	maj, err := parseUint(parts[0])
	if err != nil {
		return nil, err
	}
	min, err := parseUint(parts[1])
	if err != nil {
		return nil, err
	}
	patch, err := parseUint(parts[2])
	if err != nil {
		return nil, err
	}

	switch {
	case maj > 0: // ^1.2.3 → >=1.2.3 <2.0.0
		return rangePreds(maj, min, patch, maj+1, 0, 0), nil
	case min > 0: // ^0.2.3 → >=0.2.3 <0.3.0
		return rangePreds(0, min, patch, 0, min+1, 0), nil
	default: // ^0.0.3 → >=0.0.3 <0.0.4
		return rangePreds(0, 0, patch, 0, 0, patch+1), nil
	}
}

// parseXRange handles 1.2.x, 1.x.x, 1.x, x
func parseXRange(s string) ([]predicate, error) {
	parts := strings.Split(s, ".")

	isWild := func(p string) bool {
		return p == "x" || p == "X" || p == "*"
	}

	switch {
	case len(parts) == 1 || (len(parts) >= 1 && isWild(parts[0])):
		// x or * → match all
		return []predicate{}, nil

	case len(parts) == 2 || (len(parts) == 3 && isWild(parts[1])):
		// 1.x or 1.x.x → >=1.0.0 <2.0.0
		maj, err := parseUint(parts[0])
		if err != nil {
			return nil, err
		}
		return rangePreds(maj, 0, 0, maj+1, 0, 0), nil

	case len(parts) == 3 && isWild(parts[2]):
		// 1.2.x → >=1.2.0 <1.3.0
		maj, err := parseUint(parts[0])
		if err != nil {
			return nil, err
		}
		min, err := parseUint(parts[1])
		if err != nil {
			return nil, err
		}
		return rangePreds(maj, min, 0, maj, min+1, 0), nil
	}

	return nil, fmt.Errorf("invalid x-range: %s", s)
}

// rangePreds creates a [>=lo, <hi] predicate pair.
func rangePreds(loMaj, loMin, loPatch, hiMaj, hiMin, hiPatch uint64) []predicate {
	lo := Version{Major: loMaj, Minor: loMin, Patch: loPatch}
	hi := Version{Major: hiMaj, Minor: hiMin, Patch: hiPatch}
	return []predicate{
		{op: ">=", ver: lo},
		{op: "<", ver: hi},
	}
}

// parsePartial parses a version that may omit minor/patch (fills with 0).
func parsePartial(s string) (Version, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")

	// Strip build / prerelease for partial parsing
	pre := ""
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		s = s[:idx]
	}

	parts := strings.Split(s, ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}

	maj, err := parseUint(parts[0])
	if err != nil {
		return Version{}, err
	}
	min, err := parseUint(parts[1])
	if err != nil {
		return Version{}, err
	}
	patch, err := parseUint(parts[2])
	if err != nil {
		return Version{}, err
	}

	return Version{Major: maj, Minor: min, Patch: patch, PreRelease: pre}, nil
}

// ─── Pre-release comparison ───────────────────────────────────────────────────

// cmpPreRelease compares two pre-release strings per semver spec:
//   - release (no pre) > any pre-release
//   - identifiers compared left-to-right
//   - numeric identifiers < alphanumeric; pure numerics compared as ints
func cmpPreRelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1 // release > pre-release
	}
	if b == "" {
		return -1
	}

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		aNum, aErr := strconv.ParseUint(aParts[i], 10, 64)
		bNum, bErr := strconv.ParseUint(bParts[i], 10, 64)

		switch {
		case aErr == nil && bErr == nil: // both numeric
			if c := cmpUint(aNum, bNum); c != 0 {
				return c
			}
		case aErr == nil: // numeric < alphanumeric
			return -1
		case bErr == nil:
			return 1
		default: // both alphanumeric
			if c := strings.Compare(aParts[i], bParts[i]); c != 0 {
				return c
			}
		}
	}

	return cmpInt(len(aParts), len(bParts))
}

// ─── Small utilities ─────────────────────────────────────────────────────────

func parseUint(s string) (uint64, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid non-negative integer", s)
	}
	return n, nil
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
