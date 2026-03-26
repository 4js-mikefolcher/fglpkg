package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// Default registry base URL. Override with FGLPKG_REGISTRY env var.
const defaultRegistry = "https://registry.fglpkg.dev"

// PackageInfo is the resolved metadata for a specific package version.
type PackageInfo struct {
	Name             string                    `json:"name"`
	Version          string                    `json:"version"`
	Description      string                    `json:"description"`
	DownloadURL      string                    `json:"downloadUrl"`
	Checksum         string                    `json:"checksum"` // SHA256 hex
	// GeneroConstraint declares which Genero BDL runtime versions this package
	// supports, using semver constraint syntax e.g. ">=3.20.0 <5.0.0".
	GeneroConstraint string                    `json:"genero,omitempty"`
	FGLDeps          map[string]string         `json:"fglDeps,omitempty"`
	JavaDeps         []manifest.JavaDependency `json:"javaDeps,omitempty"`
}

// VersionEntry pairs a version string with its declared Genero compatibility.
type VersionEntry struct {
	Version          string `json:"version"`
	GeneroConstraint string `json:"genero,omitempty"`
}

// VersionList is the registry response listing all available versions of a package.
type VersionList struct {
	Name           string         `json:"name"`
	Versions       []string       `json:"versions"`       // kept for backward compat
	VersionEntries []VersionEntry `json:"versionEntries"` // preferred: includes Genero info
}

// SearchResult is one entry returned by a registry search.
type SearchResult struct {
	Name          string `json:"name"`
	LatestVersion string `json:"latestVersion"`
	Description   string `json:"description"`
	Author        string `json:"author"`
}

// ─── Public API ──────────────────────────────────────────────────────────────

// Resolve fetches the best matching version of a package for the given constraint.
// constraint may be "latest", "*", or any semver constraint string (e.g. "^1.2.0").
func Resolve(name, constraint string) (*PackageInfo, error) {
	vl, err := FetchVersionList(name)
	if err != nil {
		return nil, err
	}

	candidates := make([]semver.Version, 0, len(vl.Versions))
	for _, vs := range vl.Versions {
		v, err := semver.Parse(vs)
		if err != nil {
			continue // skip malformed entries from registry
		}
		candidates = append(candidates, v)
	}

	c, err := semver.ParseConstraint(constraint)
	if err != nil {
		return nil, fmt.Errorf("invalid version constraint %q: %w", constraint, err)
	}

	best, err := c.Latest(candidates)
	if err != nil {
		return nil, fmt.Errorf("no version of %q satisfies constraint %q", name, constraint)
	}

	return FetchInfo(name, best.String())
}

// FetchVersionList retrieves all published versions for a named package.
func FetchVersionList(name string) (*VersionList, error) {
	base := registryBase()
	u := fmt.Sprintf("%s/packages/%s/versions", base, url.PathEscape(name))
	data, err := httpGet(u)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch version list for %q: %w", name, err)
	}
	var vl VersionList
	if err := json.Unmarshal(data, &vl); err != nil {
		return nil, fmt.Errorf("invalid version list response: %w", err)
	}
	return &vl, nil
}

// FetchInfo retrieves full package metadata for an exact name@version.
func FetchInfo(name, version string) (*PackageInfo, error) {
	base := registryBase()
	u := fmt.Sprintf("%s/packages/%s/%s", base, url.PathEscape(name), url.PathEscape(version))
	data, err := httpGet(u)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch package info for %s@%s: %w", name, version, err)
	}
	var info PackageInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("invalid package info response: %w", err)
	}
	return &info, nil
}

// Search queries the registry for packages matching term.
func Search(term string) ([]SearchResult, error) {
	base := registryBase()
	u := fmt.Sprintf("%s/search?q=%s", base, url.QueryEscape(term))
	data, err := httpGet(u)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	var results []SearchResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("invalid registry response: %w", err)
	}
	return results, nil
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

func registryBase() string {
	if r := os.Getenv("FGLPKG_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return defaultRegistry
}

func httpGet(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read registry response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("package not found in registry")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
