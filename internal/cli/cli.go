package cli

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
	"github.com/4js-mikefolcher/fglpkg/internal/env"
	"github.com/4js-mikefolcher/fglpkg/internal/installer"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/workspace"
)

// Execute is the main CLI entry point. It dispatches subcommands.
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
	case "list":
		return cmdList(args)
	case "env":
		return cmdEnv(args)
	case "search":
		return cmdSearch(args)
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

		return cmdWorkspace(args)
		fmt.Println("fglpkg version 0.1.0")
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command: %q\nRun 'fglpkg help' for usage", cmd)
	}
}

// cmdInit creates a new fglpkg.json in the current directory.
func cmdInit(_ []string) error {
	if _, err := os.Stat(manifest.Filename); err == nil {
		return fmt.Errorf("%s already exists in the current directory", manifest.Filename)
	}

	// Prompt for basic info
	name := promptWithDefault("Package name", filepath_base())
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

// cmdInstall installs packages. With no args, installs all deps from fglpkg.json.
// With args, adds and installs the specified packages.
func cmdInstall(args []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}

	inst := installer.New(home)
	projectDir, _ := os.Getwd()

	if len(args) == 0 {
		// Install all dependencies, honouring the lock file if present.
		m, err := manifest.Load(".")
		if err != nil {
			return fmt.Errorf("failed to load %s: %w\nRun 'fglpkg init' first", manifest.Filename, err)
		}
		return inst.InstallAll(m, projectDir, false)
	}

	// Install specific packages, add to fglpkg.json, then re-lock.
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

	// Re-resolve and re-lock with the updated manifest.
	fmt.Println()
	return inst.InstallAll(m, projectDir, true)
}

// cmdUpdate re-resolves all deps ignoring the lock file and writes a fresh lock.
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
	return installer.New(home).InstallAll(m, projectDir, true)
}

// cmdPublish packages the current directory and publishes it to the registry.
func cmdPublish(_ []string) error {
	m, err := manifest.Load(".")
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("manifest is invalid: %w", err)
	}

	token := credentials.TokenFor(home, registryURL)
	if token == "" {
		return fmt.Errorf("not logged in to %s\nRun 'fglpkg login' or set FGLPKG_PUBLISH_TOKEN", registryURL)
	}

	registryURL := os.Getenv("FGLPKG_REGISTRY")
	if registryURL == "" {
		registryURL = "https://registry.fglpkg.dev"
	}

	fmt.Printf("Publishing %s@%s to %s...\n", m.Name, m.Version, registryURL)

	if err := publishPackage(m, token, registryURL); err != nil {
		return fmt.Errorf("publish failed: %w", err)
	}

	fmt.Printf("✓ Published %s@%s\n", m.Name, m.Version)
	return nil
}

// publishPackage builds and uploads the package zip.
func publishPackage(m *manifest.Manifest, token, registryURL string) error {
	// Build zip of the current directory's compiled files.
	zipData, checksum, err := buildPackageZip(m)
	if err != nil {
		return fmt.Errorf("cannot build package zip: %w", err)
	}
	fmt.Printf("  Package zip: %d bytes (SHA256: %s)\n", len(zipData), checksum)

	// Build metadata JSON.
	meta := map[string]any{
		"description": m.Description,
		"author":      m.Author,
		"license":     m.License,
		"genero":      m.GeneroConstraint,
		"fglDeps":     m.Dependencies.FGL,
		"checksum":    checksum,
	}
	if len(m.Dependencies.Java) > 0 {
		meta["javaDeps"] = m.Dependencies.Java
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	// Build multipart body.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("meta", string(metaJSON)) //nolint:errcheck
	fw, err := mw.CreateFormFile("zip", m.Name+"-"+m.Version+".zip")
	if err != nil {
		return err
	}
	fw.Write(zipData) //nolint:errcheck
	mw.Close()

	// POST to registry.
	url := fmt.Sprintf("%s/packages/%s/%s/publish",
		strings.TrimRight(registryURL, "/"), m.Name, m.Version)
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// buildPackageZip creates a zip of *.42m (and *.42f, *.sch) files in the
// current directory, plus fglpkg.json. Returns the zip bytes and its
// SHA256 checksum.
func buildPackageZip(m *manifest.Manifest) ([]byte, string, error) {
	var buf bytes.Buffer
	h := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(&buf, h))

	patterns := []string{"*.42m", "*.42f", "*.sch", manifest.Filename}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			if err := addFileToZip(zw, match); err != nil {
				return nil, "", fmt.Errorf("cannot add %s to zip: %w", match, err)
			}
		}
	}

	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), hex.EncodeToString(h.Sum(nil)), nil
}

func addFileToZip(zw *zip.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fw, err := zw.Create(filepath.Base(path))
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, f)
	return err
}


func cmdWorkspace(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg workspace <subcommand>\n" +
			"subcommands: init, add, list, info")
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "init":
		return cmdWorkspaceInit(rest)
	case "add":
		return cmdWorkspaceAdd(rest)
	case "list":
		return cmdWorkspaceList()
	case "info":
		return cmdWorkspaceInfo()
	default:
		return fmt.Errorf("unknown workspace subcommand %q", sub)
	}
}

// cmdWorkspaceInit creates a new fglpkg.workspace.json in the current directory.
func cmdWorkspaceInit(args []string) error {
	if workspace.Exists(".") {
		return fmt.Errorf("%s already exists in the current directory", workspace.WorkspaceFilename)
	}
	members := args // remaining args are member paths
	if len(members) == 0 {
		fmt.Println("No members specified — creating empty workspace.")
		fmt.Println("Use 'fglpkg workspace add <path>' to add members.")
	}
	if err := workspace.Init(".", members); err != nil {
		return err
	}
	fmt.Printf("✓ Created %s\n", workspace.WorkspaceFilename)
	if len(members) > 0 {
		fmt.Printf("  Members: %s\n", strings.Join(members, ", "))
	}
	return nil
}

// cmdWorkspaceAdd adds a new member path to the current workspace.
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

// cmdWorkspaceList lists workspace members.
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

// cmdWorkspaceInfo prints a detailed workspace summary.
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

	inst := installer.New(home)
	for _, pkg := range args {
		if err := inst.Remove(pkg); err != nil {
			return fmt.Errorf("failed to remove %s: %w", pkg, err)
		}
		m.RemoveFGLDependency(pkg)
		fmt.Printf("✓ Removed %s\n", pkg)
	}

	return m.Save(".")
}

// cmdList shows all installed packages.
func cmdList(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}

	inst := installer.New(home)
	pkgs, err := inst.List()
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

// cmdEnv prints the environment variable exports needed for Genero BDL.
func cmdEnv(_ []string) error {
	home, err := fglpkgHome()
	if err != nil {
		return err
	}

	e := env.New(home)
	exports, err := e.Generate()
	if err != nil {
		return err
	}

	for _, line := range exports {
		fmt.Println(line)
	}
	return nil
}

// cmdSearch searches the registry for packages.
func cmdSearch(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fglpkg search <term>")
	}

	term := args[0]
	results, err := registry.Search(term)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Printf("No packages found matching %q\n", term)
		return nil
	}

	fmt.Printf("Results for %q:\n", term)
	fmt.Printf("  %-30s %-12s %s\n", "NAME", "VERSION", "DESCRIPTION")
	fmt.Printf("  %-30s %-12s %s\n", "----", "-------", "-----------")
	for _, r := range results {
		fmt.Printf("  %-30s %-12s %s\n", r.Name, r.LatestVersion, r.Description)
	}
	return nil
}

// --- Helpers ---

func printUsage() {
	fmt.Print(`fglpkg - Genero BDL Package Manager

USAGE:
  fglpkg <command> [arguments]

COMMANDS:
  init              Create a new fglpkg.json in the current directory
  install           Install all dependencies from fglpkg.json
  install <pkg>     Add and install a specific package
  remove <pkg>      Remove a package
  list              List installed packages
  env               Print environment variable exports (use with eval)
  search <term>     Search the package registry
  version           Print fglpkg version
  help              Show this help message

ENVIRONMENT SETUP:
  Add this to your .bashrc or .profile:
    eval "$(fglpkg env)"

EXAMPLES:
  fglpkg init
  fglpkg install myutils
  fglpkg install myutils@1.2.0
  fglpkg env
  fglpkg search json

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

func parsePackageArg(arg string) (name, version string, err error) {
	for i, c := range arg {
		if c == '@' && i > 0 {
			return arg[:i], arg[i+1:], nil
		}
	}
	return arg, "latest", nil
}

func filepath_base() string {
	dir, _ := os.Getwd()
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' || dir[i] == '\\' {
			return dir[i+1:]
		}
	}
	return dir
}

func promptWithDefault(label, def string) string {
	if def != "" {
		fmt.Printf("%s (%s): ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	var val string
	fmt.Scanln(&val)
	if val == "" {
		return def
	}
	return val
}
