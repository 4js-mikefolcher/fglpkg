package semver

// MustParseAll parses a list of version strings and panics on any error.
// Primarily useful in tests and static initialisation.
func MustParseAll(versions ...string) []Version {
	out := make([]Version, len(versions))
	for i, s := range versions {
		out[i] = MustParse(s)
	}
	return out
}

// MustParseConstraint parses a constraint string and panics on error.
func MustParseConstraint(s string) Constraint {
	c, err := ParseConstraint(s)
	if err != nil {
		panic(err)
	}
	return c
}

// Sort sorts a slice of versions in ascending order (in place).
func Sort(vs []Version) {
	n := len(vs)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && vs[j].LessThan(vs[j-1]); j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}

// Filter returns only those versions from vs that satisfy c.
func Filter(vs []Version, c Constraint) []Version {
	out := vs[:0:0]
	for _, v := range vs {
		if c.Matches(v) {
			out = append(out, v)
		}
	}
	return out
}
