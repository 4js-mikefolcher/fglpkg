package cli

import "testing"

func TestMatchGlobSimplePattern(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.md", "README.md", true},
		{"*.md", "docs/guide.md", true},     // basename fallback
		{"*.md", "README.txt", false},
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", true}, // basename fallback
		{"*.go", "main.go", true},
		{"*.go", "cmd/main.go", true},
		{"*.go", "main.rs", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestMatchGlobDoublestar(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"docs/**/*.md", "docs/guide.md", true},
		{"docs/**/*.md", "docs/api/guide.md", true},
		{"docs/**/*.md", "docs/api/v2/guide.md", true},
		{"docs/**/*.md", "docs/guide.txt", false},
		{"docs/**/*.md", "src/guide.md", false},
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/guide.md", true},
		{"**/*.md", "a/b/c/deep.md", true},
		{"**/*.md", "test.txt", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestMatchGlobDoublestarNoSuffix(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"docs/**", "docs/guide.md", true},
		{"docs/**", "docs/api/guide.md", true},
		{"docs/**", "src/guide.md", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestMatchGlobExactFile(t *testing.T) {
	if !matchGlob("CHANGELOG.md", "CHANGELOG.md") {
		t.Error("expected exact match for CHANGELOG.md")
	}
	if matchGlob("CHANGELOG.md", "OTHER.md") {
		t.Error("did not expect match for OTHER.md")
	}
}
