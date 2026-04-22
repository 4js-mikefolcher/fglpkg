// Package credentials manages per-registry authentication tokens stored in
// ~/.fglpkg/credentials.json.
//
// The file maps registry base URLs to raw tokens. It is written with mode 0600
// so other users on the same machine cannot read it.
//
// Format:
//
//	{
//	  "registries": {
//	    "https://fglpkg-registry.fly.dev": {
//	      "token": "<raw token>",
//	      "username": "alice",
//	      "savedAt": "2026-03-25T10:00:00Z"
//	    }
//	  }
//	}
package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const filename = "credentials.json"

// Entry holds the stored credential for one registry.
type Entry struct {
	Token       string `json:"token"`
	Username    string `json:"username,omitempty"`
	GitHubToken string `json:"githubToken,omitempty"`
	SavedAt     string `json:"savedAt"`
}

// File is the top-level credentials file structure.
type File struct {
	Registries map[string]Entry `json:"registries"`
}

// Load reads the credentials file from the fglpkg home directory.
// Returns an empty File if it does not exist yet.
func Load(home string) (*File, error) {
	path := filepath.Join(home, filename)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &File{Registries: make(map[string]Entry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read credentials: %w", err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("invalid credentials file: %w", err)
	}
	if f.Registries == nil {
		f.Registries = make(map[string]Entry)
	}
	return &f, nil
}

// Save writes the credentials file with mode 0600.
func (f *File) Save(home string) error {
	if err := os.MkdirAll(home, 0700); err != nil {
		return fmt.Errorf("cannot create credentials directory: %w", err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(home, filename)
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// Set stores a token for the given registry URL.
func (f *File) Set(registryURL, token, username string) {
	f.Registries[normalise(registryURL)] = Entry{
		Token:    token,
		Username: username,
		SavedAt:  time.Now().UTC().Format(time.RFC3339),
	}
}

// Get retrieves the credential for registryURL, or returns a zero Entry and
// false if none is stored.
func (f *File) Get(registryURL string) (Entry, bool) {
	e, ok := f.Registries[normalise(registryURL)]
	return e, ok
}

// Delete removes the credential for registryURL.
func (f *File) Delete(registryURL string) {
	delete(f.Registries, normalise(registryURL))
}

// TokenFor returns the raw token for registryURL, or "" if not found.
// It also checks the FGLPKG_PUBLISH_TOKEN env var as a fallback, so CI
// environments work without a credentials file.
func TokenFor(home, registryURL string) string {
	// Env var takes precedence (useful in CI).
	if t := os.Getenv("FGLPKG_PUBLISH_TOKEN"); t != "" {
		return t
	}
	f, err := Load(home)
	if err != nil {
		return ""
	}
	e, ok := f.Get(registryURL)
	if !ok {
		return ""
	}
	return e.Token
}

// SetGitHubToken stores a GitHub token for the given registry URL, preserving
// any existing registry token and username on that entry.
func (f *File) SetGitHubToken(registryURL, githubToken string) {
	key := normalise(registryURL)
	e := f.Registries[key]
	e.GitHubToken = githubToken
	if e.SavedAt == "" {
		e.SavedAt = time.Now().UTC().Format(time.RFC3339)
	}
	f.Registries[key] = e
}

// GitHubTokenFor returns the GitHub token to use for package downloads.
// It checks the FGLPKG_GITHUB_TOKEN env var first, then falls back to the
// stored credential for the default registry.
func GitHubTokenFor(home, registryURL string) string {
	if t := os.Getenv("FGLPKG_GITHUB_TOKEN"); t != "" {
		return t
	}
	f, err := Load(home)
	if err != nil {
		return ""
	}
	e, ok := f.Get(registryURL)
	if !ok {
		return ""
	}
	return e.GitHubToken
}

// normalise lowercases and strips a trailing slash from a registry URL so
// "https://Registry.Example.com/" and "https://registry.example.com" both
// map to the same key.
func normalise(url string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(url)), "/")
}
