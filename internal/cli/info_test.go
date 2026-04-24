package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLatestVersionSemverOrdering(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"single", []string{"1.0.0"}, "1.0.0"},
		{"ordered", []string{"1.0.0", "1.1.0", "1.2.0"}, "1.2.0"},
		{"unordered", []string{"2.0.0", "1.5.0", "1.10.0"}, "2.0.0"},
		{"prerelease_of_next_patch_beats_current", []string{"1.0.0", "1.0.1-alpha"}, "1.0.1-alpha"},
		{"release_beats_its_own_prerelease", []string{"2.0.0-rc.1", "2.0.0"}, "2.0.0"},
		{"empty", []string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := latestVersion(tc.in)
			if got != tc.want {
				t.Errorf("latestVersion(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// stubRegistry serves the minimal endpoints cmdInfo needs.
func stubRegistry(t *testing.T) *httptest.Server {
	versions := map[string]any{
		"name":     "demo",
		"versions": []string{"1.0.0", "1.1.0", "1.2.0"},
		"versionEntries": []map[string]any{
			{"version": "1.0.0"},
			{"version": "1.1.0"},
			{"version": "1.2.0"},
		},
	}
	byVersion := map[string]map[string]any{
		"1.2.0": {
			"name":        "demo",
			"version":     "1.2.0",
			"description": "demo package",
			"author":      "alice",
			"license":     "MIT",
			"publishedAt": "2026-04-23T10:00:00Z",
			"checksum":    "abc123",
			"downloadUrl": "https://example.com/demo-1.2.0.zip",
			"fglDeps":     map[string]string{"utils": "^1.0.0"},
			"javaDeps": []map[string]string{
				{"groupId": "com.example", "artifactId": "foo", "version": "1.0"},
			},
		},
		"1.0.0": {
			"name":        "demo",
			"version":     "1.0.0",
			"description": "demo package (old)",
			"checksum":    "oldsum",
			"downloadUrl": "https://example.com/demo-1.0.0.zip",
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/packages/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/packages/")
		parts := strings.Split(path, "/")
		// /packages/demo/versions
		if len(parts) == 2 && parts[1] == "versions" {
			_ = json.NewEncoder(w).Encode(versions)
			return
		}
		// /packages/demo/<version>
		if len(parts) == 2 {
			v, ok := byVersion[parts[1]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(v)
			return
		}
		http.NotFound(w, r)
	})
	return httptest.NewServer(mux)
}

// TestCmdInfoLatest verifies `fglpkg info demo` resolves to the newest
// version and fetches its details.
func TestCmdInfoLatest(t *testing.T) {
	ts := stubRegistry(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdInfo([]string{"demo"})
	})
	if err != nil {
		t.Fatalf("cmdInfo: %v", err)
	}

	wantSubstrings := []string{
		"demo@1.2.0 (latest)",
		"Description: demo package",
		"Author:      alice",
		"License:     MIT",
		"sha256:abc123",
		"https://example.com/demo-1.2.0.zip",
		"utils",
		"com.example:foo:1.0",
		"Versions (3): 1.0.0, 1.1.0, 1.2.0",
		"fglpkg install demo@1.2.0",
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(stdout, sub) {
			t.Errorf("output missing %q\n---\n%s", sub, stdout)
		}
	}
}

// TestCmdInfoSpecificVersion verifies `fglpkg info demo@1.0.0` does NOT
// label the result as (latest).
func TestCmdInfoSpecificVersion(t *testing.T) {
	ts := stubRegistry(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdInfo([]string{"demo@1.0.0"})
	})
	if err != nil {
		t.Fatalf("cmdInfo: %v", err)
	}
	if !strings.Contains(stdout, "demo@1.0.0") {
		t.Errorf("expected header demo@1.0.0, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "(latest)") {
		t.Errorf("explicit version should not be labelled (latest), got:\n%s", stdout)
	}
}

// TestCmdInfoJSON exercises --json output (must be valid JSON matching
// the PackageInfo shape).
func TestCmdInfoJSON(t *testing.T) {
	ts := stubRegistry(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdInfo([]string{"demo", "--json"})
	})
	if err != nil {
		t.Fatalf("cmdInfo: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n---\n%s", err, stdout)
	}
	if payload["name"] != "demo" || payload["version"] != "1.2.0" {
		t.Errorf("unexpected JSON payload: %+v", payload)
	}
}

func TestCmdInfoUsageErrors(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"no args", []string{}, "usage:"},
		{"unknown flag", []string{"--bogus"}, "unknown flag"},
		{"too many args", []string{"a", "b"}, "too many arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cmdInfo(tc.args)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// what was written. Simple enough for single-goroutine command tests.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	errCh := make(chan error, 1)
	go func() { errCh <- fn() }()

	// Close the writer once fn completes so the reader can finish.
	fnErr := <-errCh
	_ = w.Close()
	os.Stdout = orig

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, readErr := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	_ = r.Close()
	return string(buf), fnErr
}
