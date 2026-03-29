// Package github provides helpers for uploading and downloading fglpkg
// package zips via GitHub Releases on a shared repository.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const apiBase = "https://api.github.com"

// ReleaseTag returns the Git tag used for a package version.
// Example: ReleaseTag("poiapi", "1.0.0") → "poiapi-v1.0.0"
func ReleaseTag(name, version string) string {
	return name + "-v" + version
}

// AssetName returns the zip filename used for a release asset.
// Example: AssetName("poiapi", "1.0.0") → "poiapi-1.0.0.zip"
func AssetName(name, version string) string {
	return name + "-" + version + ".zip"
}

// RepoFromEnv reads FGLPKG_GITHUB_REPO and returns the owner and repo.
// Returns empty strings and a nil error if the env var is not set, allowing
// callers to fall back to the registry config.
func RepoFromEnv() (owner, repo string, err error) {
	val := os.Getenv("FGLPKG_GITHUB_REPO")
	if val == "" {
		return "", "", nil
	}
	parts := strings.SplitN(val, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("FGLPKG_GITHUB_REPO must be in owner/repo format, got %q", val)
	}
	return parts[0], parts[1], nil
}

// releaseResponse is the subset of the GitHub Release API response we need.
type releaseResponse struct {
	ID       int64  `json:"id"`
	UploadURL string `json:"upload_url"`
}

// assetResponse is the subset of the GitHub Release Asset response we need.
type assetResponse struct {
	ID  int64  `json:"id"`
	URL string `json:"url"` // API URL for downloading with Accept: application/octet-stream
}

// GetReleaseByTag looks up a release by tag. Returns the release ID or an
// error. Returns a non-nil error wrapping errNotFound if the release does
// not exist.
func GetReleaseByTag(token, owner, repo, tag string) (int64, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", apiBase, owner, repo, tag)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	setHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("release %q not found: %w", tag, errNotFound)
	}
	if err := checkResponse(resp); err != nil {
		return 0, err
	}

	var r releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, fmt.Errorf("cannot parse GitHub response: %w", err)
	}
	return r.ID, nil
}

// CreateRelease creates a new release with the given tag. The tag is created
// automatically by GitHub if it does not exist.
func CreateRelease(token, owner, repo, tag, title string) (int64, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases", apiBase, owner, repo)
	body := map[string]any{
		"tag_name": tag,
		"name":     title,
		"body":     "Published by fglpkg",
		"draft":    false,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	setHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return 0, err
	}

	var r releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, fmt.Errorf("cannot parse GitHub response: %w", err)
	}
	return r.ID, nil
}

// UploadAsset uploads zipData as a release asset and returns the API URL
// for downloading the asset (suitable for private repos with auth).
func UploadAsset(token, owner, repo string, releaseID int64, filename string, zipData []byte) (apiURL string, err error) {
	url := fmt.Sprintf("https://uploads.github.com/repos/%s/%s/releases/%d/assets?name=%s",
		owner, repo, releaseID, filename)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(zipData))
	if err != nil {
		return "", err
	}
	setHeaders(req, token)
	req.Header.Set("Content-Type", "application/zip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnprocessableEntity {
		return "", fmt.Errorf("asset %q already exists for this release — the version may already be published", filename)
	}
	if err := checkResponse(resp); err != nil {
		return "", err
	}

	var a assetResponse
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return "", fmt.Errorf("cannot parse GitHub response: %w", err)
	}
	return a.URL, nil
}

// GetOrCreateRelease returns the release ID for the given tag, creating
// the release if it does not already exist.
func GetOrCreateRelease(token, owner, repo, tag, title string) (int64, error) {
	id, err := GetReleaseByTag(token, owner, repo, tag)
	if err == nil {
		return id, nil
	}
	if !isNotFound(err) {
		return 0, err
	}
	return CreateRelease(token, owner, repo, tag, title)
}

// DeleteRelease deletes a GitHub Release by its tag. This also removes all
// assets attached to the release. Returns nil if the release does not exist.
func DeleteRelease(token, owner, repo, tag string) error {
	id, err := GetReleaseByTag(token, owner, repo, tag)
	if err != nil {
		if isNotFound(err) {
			return nil // already gone
		}
		return err
	}

	url := fmt.Sprintf("%s/repos/%s/%s/releases/%d", apiBase, owner, repo, id)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	setHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return checkResponse(resp)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

var errNotFound = fmt.Errorf("not found")

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), errNotFound.Error())
}

func setHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("GitHub authentication failed (HTTP 401): check your GitHub token")
	case http.StatusForbidden:
		return fmt.Errorf("GitHub access denied (HTTP 403): token may lack required permissions")
	case http.StatusNotFound:
		return fmt.Errorf("GitHub resource not found (HTTP 404): check FGLPKG_GITHUB_REPO and token permissions")
	default:
		return fmt.Errorf("GitHub API error (HTTP %d): %s", resp.StatusCode, string(body))
	}
}

// IsGitHubURL returns true if the URL points to the GitHub API (used to
// decide whether to attach auth headers during downloads).
func IsGitHubURL(url string) bool {
	return strings.HasPrefix(url, "https://api.github.com/")
}
