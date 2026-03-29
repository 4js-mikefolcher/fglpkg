package cli

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
	"github.com/4js-mikefolcher/fglpkg/internal/env"
	gh "github.com/4js-mikefolcher/fglpkg/internal/github"
	"github.com/4js-mikefolcher/fglpkg/internal/installer"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/workspace"
)

// Version and Build are set at compile time via -ldflags.
var (
	Version = "dev"
	Build   = "unknown"
)

// reader is a package-level buffered stdin reader shared across all prompts
// so buffered input is never lost between successive promptWithDefault calls.
var reader = bufio.NewReader(os.Stdin)

// Execute is the main CLI entry point.
func Execute() error {
	if len(os.Args) < 2 {
		printUsage()
		return nil
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "init":
		return cmdInit(args)
	case "install":
		return cmdInstall(args)
	case "remove":
		return cmdRemove(args)
	case "update":
		return cmdUpdate(args)
	case "list":
		return cmdList(args)
	case "env":
		return cmdEnv(args)
	case "search":
		return cmdSearch(args)
	case "publish":
		return cmdPublish(args)
	case "unpublish":
		return cmdUnpublish(args)
	case "login":
		return cmdLogin(args)
	case "logout":
		return cmdLogout(args)
	case "whoami":
		return cmdWhoami(args)
	case "owner":
		return cmdOwner(args)
	case "token":
		return cmdToken(args)
	case "config":
		return cmdConfig(args)
	case "workspace", "ws":
		return cmdWorkspace(args)
	case "version":
		fmt.Printf("fglpkg version %s (build %s)\n", Version, Build)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command: %q\nRun 'fglpkg help' for usage", cmd)
	}
}

// ─── init ─────────────────────────────────────────────────────────────────────

func cmdInit(_ []string) error {
	if _, err := os.Stat(manifest.Filename); err == nil {
		return fmt.Errorf("%s already exists in the current directory", manifest.Filename)
	}
	name := promptWithDefault("Package name", filepathBase())
	version := promptWithDefault("Version", "0.1.0")
	description := promptWithDefault("Description", "")
	author := promptWithDefault("Author", "")
	m := manifest.New(name, version, description, author)
	if err := m.Save("."); err != nil {
		return fmt.Errorf("failed to write %s: %w", manifest.Filename, err)
	}
	fmt.Printf("✓ Created %s\n", manifest.Filename)
	return nil
}

// ─── install ──────────────────────────────────────────────────────────────────

func cmdInstall(args []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	inst := newInstaller(home)
	projectDir, _ := os.Getwd()

	if len(args) == 0 {
		m, err := manifest.Load(".")
		if err != nil {
			return fmt.Errorf("failed to load %s: %w\nRun 'fglpkg init' first", manifest.Filename, err)
		}
		return inst.InstallAll(m, projectDir, false)
	}

	m, err := manifest.LoadOrNew(".")
	if err != nil {
		return err
	}
	for _, pkg := range args {
		name, version, err := parsePackageArg(pkg)
		if err != nil {
			return err
		}
		fmt.Printf("Resolving %s@%s...\n", name, version)
		info, err := registry.Resolve(name, version)
		if err != nil {
			return fmt.Errorf("failed to resolve %s@%s: %w", name, version, err)
		}
		m.AddFGLDependency(info.Name, info.Version)
		fmt.Printf("✓ Added %s@%s to %s\n", info.Name, info.Version, manifest.Filename)
	}
	if err := m.Save("."); err != nil {
		return err
	}
	fmt.Println()
	return inst.InstallAll(m, projectDir, true)
}

// ─── remove ───────────────────────────────────────────────────────────────────

func cmdRemove(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg remove <package>")
	}
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	m, err := manifest.Load(".")
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	inst := newInstaller(home)
	for _, pkg := range args {
		if err := inst.Remove(pkg); err != nil {
			return fmt.Errorf("failed to remove %s: %w", pkg, err)
		}
		m.RemoveFGLDependency(pkg)
		fmt.Printf("✓ Removed %s\n", pkg)
	}
	return m.Save(".")
}

// ─── update ───────────────────────────────────────────────────────────────────

func cmdUpdate(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	m, err := manifest.Load(".")
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	projectDir, _ := os.Getwd()
	fmt.Println("Ignoring lock file and re-resolving all dependencies...")
	return newInstaller(home).InstallAll(m, projectDir, true)
}

// ─── list ─────────────────────────────────────────────────────────────────────

func cmdList(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	pkgs, err := newInstaller(home).List()
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		fmt.Println("No packages installed.")
		return nil
	}
	fmt.Println("Installed packages:")
	for _, p := range pkgs {
		fmt.Printf("  %-30s %s\n", p.Name, p.Version)
	}
	return nil
}

// ─── env ──────────────────────────────────────────────────────────────────────

func cmdEnv(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	exports, err := env.New(home).Generate()
	if err != nil {
		return err
	}
	for _, line := range exports {
		fmt.Println(line)
	}
	return nil
}

// ─── search ───────────────────────────────────────────────────────────────────

func cmdSearch(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg search <term>")
	}
	results, err := registry.Search(args[0])
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}
	if len(results) == 0 {
		fmt.Printf("No packages found matching %q\n", args[0])
		return nil
	}
	fmt.Printf("Results for %q:\n", args[0])
	fmt.Printf("  %-30s %-12s %s\n", "NAME", "VERSION", "DESCRIPTION")
	fmt.Printf("  %-30s %-12s %s\n", "----", "-------", "-----------")
	for _, r := range results {
		fmt.Printf("  %-30s %-12s %s\n", r.Name, r.LatestVersion, r.Description)
	}
	return nil
}

// ─── publish ──────────────────────────────────────────────────────────────────

func cmdPublish(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	m, err := manifest.Load(".")
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("manifest is invalid: %w", err)
	}
	registryURL := defaultRegistry()
	token := credentials.TokenFor(home, registryURL)
	if token == "" {
		return fmt.Errorf("not logged in to %s\nRun 'fglpkg login' or set FGLPKG_PUBLISH_TOKEN", registryURL)
	}
	githubToken := credentials.GitHubTokenFor(home, registryURL)
	if githubToken == "" {
		return fmt.Errorf("GitHub token required for publishing\nSet FGLPKG_GITHUB_TOKEN or run 'fglpkg login'")
	}
	owner, repo, err := resolveGitHubRepo()
	if err != nil {
		return err
	}

	fmt.Printf("Publishing %s@%s to %s...\n", m.Name, m.Version, registryURL)
	if err := publishPackage(m, token, registryURL, githubToken, owner, repo); err != nil {
		return fmt.Errorf("publish failed: %w", err)
	}
	fmt.Printf("✓ Published %s@%s\n", m.Name, m.Version)
	return nil
}

func publishPackage(m *manifest.Manifest, token, registryURL, githubToken, owner, repo string) error {
	// 1. Build the zip.
	zipData, checksum, err := buildPackageZip(m)
	if err != nil {
		return fmt.Errorf("cannot build package zip: %w", err)
	}
	fmt.Printf("  Package zip: %d bytes (SHA256: %s)\n", len(zipData), checksum)

	// 2. Upload to GitHub Releases.
	tag := gh.ReleaseTag(m.Name, m.Version)
	assetName := gh.AssetName(m.Name, m.Version)
	title := fmt.Sprintf("%s v%s", m.Name, m.Version)

	fmt.Printf("  Uploading to GitHub (%s/%s)...\n", owner, repo)
	releaseID, err := gh.GetOrCreateRelease(githubToken, owner, repo, tag, title)
	if err != nil {
		return fmt.Errorf("GitHub release failed: %w", err)
	}

	downloadURL, err := gh.UploadAsset(githubToken, owner, repo, releaseID, assetName, zipData)
	if err != nil {
		return fmt.Errorf("GitHub upload failed: %w", err)
	}
	fmt.Printf("  Uploaded to GitHub: %s\n", downloadURL)

	// 3. Register metadata with the registry (JSON-only, no zip).
	meta := map[string]any{
		"description": m.Description,
		"author":      m.Author,
		"license":     m.License,
		"genero":      m.GeneroConstraint,
		"fglDeps":     m.Dependencies.FGL,
		"checksum":    checksum,
		"downloadUrl": downloadURL,
	}
	if len(m.Dependencies.Java) > 0 {
		meta["javaDeps"] = m.Dependencies.Java
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/packages/%s/%s/publish",
		strings.TrimRight(registryURL, "/"), m.Name, m.Version)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(metaJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("package uploaded to GitHub but registry metadata update failed (%d: %s)\nRe-run 'fglpkg publish' to retry",
			resp.StatusCode, string(respBody))
	}
	return nil
}

func buildPackageZip(m *manifest.Manifest) ([]byte, string, error) {
	var buf bytes.Buffer
	h := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(&buf, h))

	// Determine the root directory for package files.
	root := m.Root
	if root == "" {
		root = "."
	}

	// Use manifest's files list if specified, otherwise use defaults.
	patterns := m.Files
	if len(patterns) == 0 {
		patterns = []string{"*.42m", "*.42f", "*.sch"}
	}

	// Walk the root directory tree and collect files matching the patterns.
	added := make(map[string]bool)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		for _, pattern := range patterns {
			matched, _ := filepath.Match(pattern, base)
			if matched && !added[path] {
				added[path] = true
				// Keep the path relative to the project directory (not
				// root) so the full directory structure is preserved in
				// the zip.  When extracted into ~/.fglpkg/packages/<name>/,
				// files like com/fourjs/poiapi/Module.42m stay intact.
				relPath, relErr := filepath.Rel(".", path)
				if relErr != nil {
					relPath = path
				}
				if err := addFileToZip(zw, path, relPath); err != nil {
					return fmt.Errorf("cannot add %s to zip: %w", path, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("error walking root %q: %w", root, err)
	}

	// Always include the manifest from the current directory.
	if !added[manifest.Filename] {
		if err := addFileToZip(zw, manifest.Filename, manifest.Filename); err != nil {
			return nil, "", fmt.Errorf("cannot add %s to zip: %w", manifest.Filename, err)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), hex.EncodeToString(h.Sum(nil)), nil
}

// addFileToZip adds a file at diskPath into the zip using zipPath as
// its name, preserving directory structure.
func addFileToZip(zw *zip.Writer, diskPath, zipPath string) error {
	f, err := os.Open(diskPath)
	if err != nil {
		return err
	}
	defer f.Close()
	// Always use forward slashes in zip entries for portability.
	fw, err := zw.Create(filepath.ToSlash(zipPath))
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, f)
	return err
}

// ─── unpublish ────────────────────────────────────────────────────────────────

func cmdUnpublish(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg unpublish <package>@<version>")
	}
	name, version, err := parsePackageArg(args[0])
	if err != nil {
		return err
	}
	if version == "" || version == "latest" {
		return fmt.Errorf("a specific version is required: fglpkg unpublish <package>@<version>")
	}

	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	registryURL := defaultRegistry()
	token := credentials.TokenFor(home, registryURL)
	if token == "" {
		return fmt.Errorf("not logged in to %s\nRun 'fglpkg login' or set FGLPKG_PUBLISH_TOKEN", registryURL)
	}

	fmt.Printf("Unpublishing %s@%s...\n", name, version)

	// 1. Delete the GitHub Release (and its asset).
	githubToken := credentials.GitHubTokenFor(home, registryURL)
	if githubToken != "" {
		owner, repo, err := resolveGitHubRepo()
		if err == nil {
			tag := gh.ReleaseTag(name, version)
			fmt.Printf("  Deleting GitHub release %s...\n", tag)
			if err := gh.DeleteRelease(githubToken, owner, repo, tag); err != nil {
				fmt.Printf("  Warning: could not delete GitHub release: %v\n", err)
			} else {
				fmt.Println("  Deleted GitHub release")
			}
		}
	}

	// 2. Remove metadata from the registry.
	url := fmt.Sprintf("%s/packages/%s/%s/unpublish",
		strings.TrimRight(registryURL, "/"), name, version)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(respBody))
	}

	fmt.Printf("✓ Unpublished %s@%s\n", name, version)
	return nil
}

// ─── login ────────────────────────────────────────────────────────────────────

func cmdLogin(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	registryURL := promptWithDefault("Registry URL", defaultRegistry())
	token := promptWithDefault("Token", "")
	if token == "" {
		return fmt.Errorf("token cannot be empty")
	}
	username, err := whoamiRequest(registryURL, token)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	creds, err := credentials.Load(home)
	if err != nil {
		return err
	}
	creds.Set(registryURL, token, username)

	githubToken := promptWithDefault("GitHub token (optional, for package downloads)", "")
	if githubToken != "" {
		creds.SetGitHubToken(registryURL, githubToken)
	}

	if err := creds.Save(home); err != nil {
		return err
	}
	fmt.Printf("✓ Logged in to %s as %s\n", registryURL, username)
	if githubToken != "" {
		fmt.Println("✓ GitHub token saved for package downloads")
	} else {
		fmt.Println("  GitHub token skipped (set FGLPKG_GITHUB_TOKEN for downloads from private repos)")
	}
	return nil
}

// ─── logout ───────────────────────────────────────────────────────────────────

func cmdLogout(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	registryURL := promptWithDefault("Registry URL", defaultRegistry())
	creds, err := credentials.Load(home)
	if err != nil {
		return err
	}
	if _, ok := creds.Get(registryURL); !ok {
		fmt.Printf("Not logged in to %s\n", registryURL)
		return nil
	}
	creds.Delete(registryURL)
	if err := creds.Save(home); err != nil {
		return err
	}
	fmt.Printf("✓ Logged out from %s\n", registryURL)
	return nil
}

// ─── whoami ───────────────────────────────────────────────────────────────────

func cmdWhoami(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}
	registryURL := defaultRegistry()
	token := credentials.TokenFor(home, registryURL)
	if token == "" {
		return fmt.Errorf("not logged in to %s\nRun 'fglpkg login' first", registryURL)
	}
	username, err := whoamiRequest(registryURL, token)
	if err != nil {
		return fmt.Errorf("whoami failed: %w", err)
	}
	fmt.Printf("Logged in to %s as %s\n", registryURL, username)
	ghToken := credentials.GitHubTokenFor(home, registryURL)
	if ghToken != "" {
		fmt.Println("GitHub token: configured")
	} else {
		fmt.Println("GitHub token: not configured (set FGLPKG_GITHUB_TOKEN or run fglpkg login)")
	}
	return nil
}

// ─── owner ────────────────────────────────────────────────────────────────────

func cmdOwner(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg owner <list|add|remove> <package> [username]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		if len(rest) == 0 {
			return fmt.Errorf("usage: fglpkg owner list <package>")
		}
		return cmdOwnerList(rest[0])
	case "add":
		if len(rest) < 2 {
			return fmt.Errorf("usage: fglpkg owner add <package> <username>")
		}
		return cmdOwnerAdd(rest[0], rest[1])
	case "remove":
		if len(rest) < 2 {
			return fmt.Errorf("usage: fglpkg owner remove <package> <username>")
		}
		return cmdOwnerRemove(rest[0], rest[1])
	default:
		return fmt.Errorf("unknown owner subcommand %q", sub)
	}
}

func cmdOwnerList(pkg string) error {
	home, _ := fglpkgHome()
	reg := defaultRegistry()
	token := credentials.TokenFor(home, reg)
	resp, err := authGet(reg+"/packages/"+pkg+"/owners", token)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	var result struct {
		Owners []string `json:"owners"`
	}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	fmt.Printf("Owners of %s:\n", pkg)
	for _, o := range result.Owners {
		fmt.Printf("  %s\n", o)
	}
	return nil
}

func cmdOwnerAdd(pkg, username string) error {
	home, _ := fglpkgHome()
	reg := defaultRegistry()
	token := credentials.TokenFor(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}
	body := fmt.Sprintf(`{"username":%q}`, username)
	resp, err := authPost(reg+"/packages/"+pkg+"/owners", token, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	fmt.Printf("✓ Added %s as owner of %s\n", username, pkg)
	return nil
}

func cmdOwnerRemove(pkg, username string) error {
	home, _ := fglpkgHome()
	reg := defaultRegistry()
	token := credentials.TokenFor(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}
	req, _ := http.NewRequest(http.MethodDelete,
		reg+"/packages/"+pkg+"/owners/"+username, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	fmt.Printf("✓ Removed %s from owners of %s\n", username, pkg)
	return nil
}

// ─── token ────────────────────────────────────────────────────────────────────

func cmdToken(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg token <create|revoke|rotate> [username]")
	}
	sub, rest := args[0], args[1:]
	home, _ := fglpkgHome()
	reg := defaultRegistry()
	token := credentials.TokenFor(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}

	switch sub {
	case "create":
		username := ""
		if len(rest) > 0 {
			username = rest[0]
		} else {
			username = promptWithDefault("New username", "")
		}
		email := promptWithDefault("Email (optional)", "")
		body := fmt.Sprintf(`{"username":%q,"email":%q}`, username, email)
		resp, err := authPost(reg+"/auth/token", token, body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return registryError(resp)
		}
		var result map[string]string
		json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
		fmt.Printf("✓ Created user %s\nToken: %s\n⚠ Save this token — it will not be shown again.\n",
			result["username"], result["token"])

	case "revoke":
		target := ""
		if len(rest) > 0 {
			target = rest[0]
		}
		body := ""
		if target != "" {
			body = fmt.Sprintf(`{"username":%q}`, target)
		}
		resp, err := authDo(http.MethodDelete, reg+"/auth/token", token, body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return registryError(resp)
		}
		if target != "" {
			fmt.Printf("✓ Revoked token for %s\n", target)
		} else {
			fmt.Println("✓ Token revoked")
		}

	case "rotate":
		resp, err := authPost(reg+"/auth/token/rotate", token, "")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return registryError(resp)
		}
		var result map[string]string
		json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
		fmt.Printf("✓ Token rotated\nNew token: %s\n⚠ Save this token — it will not be shown again.\n",
			result["token"])

	default:
		return fmt.Errorf("unknown token subcommand %q", sub)
	}
	return nil
}

// ─── config ───────────────────────────────────────────────────────────────────

func cmdConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg config <github-repos> <list|add|remove> [owner/repo]")
	}
	switch args[0] {
	case "github-repos":
		return cmdConfigGitHubRepos(args[1:])
	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func cmdConfigGitHubRepos(args []string) error {
	if len(args) == 0 {
		return cmdConfigGitHubReposList()
	}
	switch args[0] {
	case "list":
		return cmdConfigGitHubReposList()
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: fglpkg config github-repos add <owner/repo>")
		}
		return cmdConfigGitHubReposAdd(args[1])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: fglpkg config github-repos remove <owner/repo>")
		}
		return cmdConfigGitHubReposRemove(args[1])
	default:
		return fmt.Errorf("unknown github-repos subcommand %q", args[0])
	}
}

func cmdConfigGitHubReposList() error {
	cfg, err := registry.FetchConfig()
	if err != nil {
		return err
	}
	if len(cfg.GitHubRepos) == 0 {
		fmt.Println("No GitHub repos configured.")
		return nil
	}
	fmt.Println("GitHub package repos:")
	for _, r := range cfg.GitHubRepos {
		fmt.Printf("  %s/%s\n", r.Owner, r.Repo)
	}
	return nil
}

func cmdConfigGitHubReposAdd(ownerRepo string) error {
	owner, repo, err := parseOwnerRepo(ownerRepo)
	if err != nil {
		return err
	}
	home, _ := fglpkgHome()
	reg := defaultRegistry()
	token := credentials.TokenFor(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}
	body := fmt.Sprintf(`{"owner":%q,"repo":%q}`, owner, repo)
	resp, err := authPost(reg+"/config/github-repos", token, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	fmt.Printf("✓ Added GitHub repo %s/%s\n", owner, repo)
	return nil
}

func cmdConfigGitHubReposRemove(ownerRepo string) error {
	owner, repo, err := parseOwnerRepo(ownerRepo)
	if err != nil {
		return err
	}
	home, _ := fglpkgHome()
	reg := defaultRegistry()
	token := credentials.TokenFor(home, reg)
	if token == "" {
		return fmt.Errorf("not logged in — run 'fglpkg login'")
	}
	url := fmt.Sprintf("%s/config/github-repos/%s/%s", reg, owner, repo)
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryError(resp)
	}
	fmt.Printf("✓ Removed GitHub repo %s/%s\n", owner, repo)
	return nil
}

func parseOwnerRepo(s string) (owner, repo string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected owner/repo format, got %q", s)
	}
	return parts[0], parts[1], nil
}

// ─── workspace ────────────────────────────────────────────────────────────────

func cmdWorkspace(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg workspace <init|add|list|info>")
	}
	switch args[0] {
	case "init":
		return cmdWorkspaceInit(args[1:])
	case "add":
		return cmdWorkspaceAdd(args[1:])
	case "list":
		return cmdWorkspaceList()
	case "info":
		return cmdWorkspaceInfo()
	default:
		return fmt.Errorf("unknown workspace subcommand %q", args[0])
	}
}

func cmdWorkspaceInit(args []string) error {
	if workspace.Exists(".") {
		return fmt.Errorf("%s already exists in the current directory", workspace.WorkspaceFilename)
	}
	if err := workspace.Init(".", args); err != nil {
		return err
	}
	fmt.Printf("✓ Created %s\n", workspace.WorkspaceFilename)
	return nil
}

func cmdWorkspaceAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg workspace add <path>")
	}
	wsRoot := workspace.FindRoot(".")
	if wsRoot == "" {
		return fmt.Errorf("not inside a workspace — run 'fglpkg workspace init' first")
	}
	for _, path := range args {
		if err := workspace.AddMember(wsRoot, path); err != nil {
			return err
		}
		fmt.Printf("✓ Added %q to workspace\n", path)
	}
	return nil
}

func cmdWorkspaceList() error {
	wsRoot := workspace.FindRoot(".")
	if wsRoot == "" {
		return fmt.Errorf("not inside a workspace")
	}
	ws, err := workspace.Load(wsRoot)
	if err != nil {
		return err
	}
	fmt.Printf("Workspace: %s\n", wsRoot)
	for _, m := range ws.Members {
		fmt.Printf("  %-30s v%s\n", m.Manifest.Name, m.Manifest.Version)
	}
	return nil
}

func cmdWorkspaceInfo() error {
	wsRoot := workspace.FindRoot(".")
	if wsRoot == "" {
		return fmt.Errorf("not inside a workspace")
	}
	ws, err := workspace.Load(wsRoot)
	if err != nil {
		return err
	}
	fmt.Print(ws.Summary())
	return nil
}

// ─── Auth HTTP helpers ────────────────────────────────────────────────────────

func whoamiRequest(registryURL, token string) (string, error) {
	resp, err := authGet(strings.TrimRight(registryURL, "/")+"/auth/whoami", token)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("invalid token")
	}
	if resp.StatusCode != http.StatusOK {
		return "", registryError(resp)
	}
	var result struct {
		Username string `json:"username"`
	}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	return result.Username, nil
}

func authGet(url, token string) (*http.Response, error) {
	return authDo(http.MethodGet, url, token, "")
}

func authPost(url, token, body string) (*http.Response, error) {
	return authDo(http.MethodPost, url, token, body)
}

func authDo(method, url, token, body string) (*http.Response, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

func registryError(resp *http.Response) error {
	var e struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e) //nolint:errcheck
	if e.Error != "" {
		return fmt.Errorf("registry error (%d): %s", resp.StatusCode, e.Error)
	}
	return fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
}

// ─── Utilities ────────────────────────────────────────────────────────────────

func printUsage() {
	fmt.Print(`fglpkg - Genero BDL Package Manager

USAGE:
  fglpkg <command> [arguments]

COMMANDS:
  init              Create a new fglpkg.json
  install           Install all dependencies (or add a specific package)
  remove <pkg>      Remove a package
  update            Re-resolve and update all dependencies
  list              List installed packages
  env               Print environment variable exports
  search <term>     Search the registry
  publish           Publish current package to registry
  unpublish <p>@<v> Remove a published version from registry + GitHub
  login             Save registry credentials
  logout            Remove saved credentials
  whoami            Show current authenticated user
  owner             Manage package ownership
  token             Manage user tokens (admin)
  config            Manage registry configuration (GitHub repos)
  workspace         Manage monorepo workspaces
  version           Print fglpkg version
  help              Show this help

ENVIRONMENT:
  FGLPKG_HOME            Override ~/.fglpkg
  FGLPKG_REGISTRY        Override default registry URL
  FGLPKG_PUBLISH_TOKEN   Admin/publish token (bypasses credentials file)
  FGLPKG_GITHUB_TOKEN    GitHub PAT for package uploads/downloads (private repo)
  FGLPKG_GITHUB_REPO     GitHub owner/repo for package storage
  FGLPKG_GENERO_VERSION  Override Genero version detection

SETUP:
  Add to ~/.bashrc:  eval "$(fglpkg env)"

`)
}

func fglpkgHome() (string, error) {
	if h := os.Getenv("FGLPKG_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return home + "/.fglpkg", nil
}

// resolveGitHubRepo returns the GitHub owner/repo for package storage.
// Precedence: FGLPKG_GITHUB_REPO env var > registry config > error.
func resolveGitHubRepo() (owner, repo string, err error) {
	owner, repo, err = gh.RepoFromEnv()
	if err != nil {
		return "", "", err
	}
	if owner != "" {
		return owner, repo, nil
	}
	// Fall back to the registry config.
	cfg, err := registry.FetchConfig()
	if err != nil {
		return "", "", fmt.Errorf("cannot determine GitHub repo: FGLPKG_GITHUB_REPO is not set and registry config is unavailable: %w", err)
	}
	if len(cfg.GitHubRepos) == 0 {
		return "", "", fmt.Errorf("no GitHub repos configured on the registry\nSet FGLPKG_GITHUB_REPO or ask an admin to run: fglpkg config github-repos add <owner/repo>")
	}
	return cfg.GitHubRepos[0].Owner, cfg.GitHubRepos[0].Repo, nil
}

func newInstaller(home string) *installer.Installer {
	registryURL := defaultRegistry()
	githubToken := credentials.GitHubTokenFor(home, registryURL)
	return installer.New(home, githubToken)
}

func defaultRegistry() string {
	if r := os.Getenv("FGLPKG_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return "https://registry.fglpkg.dev"
}

func parsePackageArg(arg string) (name, version string, err error) {
	for i, c := range arg {
		if c == '@' && i > 0 {
			return arg[:i], arg[i+1:], nil
		}
	}
	return arg, "latest", nil
}

func filepathBase() string {
	dir, _ := os.Getwd()
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' || dir[i] == '\\' {
			return dir[i+1:]
		}
	}
	return dir
}

// promptWithDefault prints a prompt and reads a full line from stdin,
// supporting spaces in the input. Returns def if the user presses enter
// without typing anything.
func promptWithDefault(label, def string) string {
	if def != "" {
		fmt.Printf("%s (%s): ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	val, err := reader.ReadString('\n')
	if err != nil && len(val) == 0 {
		return def
	}
	// Trim CR and LF to handle both Unix (\n) and Windows (\r\n) line endings.
	val = strings.TrimRight(val, "\r\n")
	if val == "" {
		return def
	}
	return val
}
