package installer

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/checksum"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

// InstalledPackage is a summary of an installed BDL package.
type InstalledPackage struct {
	Name    string
	Version string
}

// Installer manages package installation into the fglpkg home directory.
type Installer struct {
	home        string // e.g. ~/.fglpkg
	packagesDir string // ~/.fglpkg/packages
	jarsDir     string // ~/.fglpkg/jars
}

// New creates an Installer rooted at home.
func New(home string) *Installer {
	return &Installer{
		home:        home,
		packagesDir: filepath.Join(home, "packages"),
		jarsDir:     filepath.Join(home, "jars"),
	}
}

// InstallAll resolves or reads from the lock file, then installs every
// BDL package and Java JAR. If a valid lock file exists and matches the
// current environment, it is used directly (no network resolution needed).
// Pass forceResolve=true to bypass the lock and re-resolve from scratch
// (used by `fglpkg update`).
func (i *Installer) InstallAll(m *manifest.Manifest, projectDir string, forceResolve bool) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	// Detect Genero version once — used for both lock validation and resolution.
	gv, err := genero.Detect()
	if err != nil {
		return fmt.Errorf("cannot detect Genero version: %w", err)
	}

	// ── Try to use an existing lock file ────────────────────────────────────
	if !forceResolve && lockfile.Exists(projectDir) {
		lf, err := lockfile.Load(projectDir)
		if err != nil {
			fmt.Printf("warning: cannot read lock file: %v — re-resolving\n", err)
		} else {
			vr := lf.Validate(m, gv.String(), i.packagesDir)
			if vr.NeedsResolve() {
				fmt.Printf("Lock file is stale (%v) — re-resolving...\n", vr.ManifestMismatch)
			} else {
				if vr.GeneroMismatch != nil {
					fmt.Printf("warning: %v\n", vr.GeneroMismatch)
				}
				if vr.IsClean() {
					fmt.Printf("Lock file is up to date (Genero %s). Nothing to install.\n", gv)
					return nil
				}
				// Lock is valid but some packages are missing on disk — install them.
				fmt.Printf("Installing from lock file (Genero %s)...\n", gv)
				return i.installFromLock(lf)
			}
		}
	}

	// ── Resolve the full dependency graph ───────────────────────────────────
	fmt.Printf("Resolving dependency graph (Genero %s)...\n", gv)
	r, err := resolver.New()
	if err != nil {
		return fmt.Errorf("cannot initialise resolver: %w", err)
	}
	plan, err := r.Resolve(m)
	if err != nil {
		return fmt.Errorf("dependency resolution failed:\n%w", err)
	}
	fmt.Printf("Resolved %d package(s), %d JAR(s)\n\n", len(plan.Packages), len(plan.JARs))

	// Write the lock file before installing so it's always present even if
	// installation is interrupted partway through.
	lf := lockfile.FromPlan(plan, m)
	if err := lf.Save(projectDir); err != nil {
		// Non-fatal: warn but continue with the install.
		fmt.Printf("warning: could not write lock file: %v\n", err)
	} else {
		fmt.Printf("Wrote %s\n\n", lockfile.Filename)
	}

	return i.installFromPlan(plan)
}

// installFromLock installs every entry in the lock file using its pinned
// URLs and checksums, bypassing the resolver entirely.
func (i *Installer) installFromLock(lf *lockfile.LockFile) error {
	pkgs, jars := lf.ToInstallList()

	for _, pkg := range pkgs {
		if _, err := os.Stat(filepath.Join(i.packagesDir, pkg.Name)); err == nil {
			fmt.Printf("  ✓ %s@%s (already installed)\n", pkg.Name, pkg.Version)
			continue
		}
		fmt.Printf("  → %s@%s\n", pkg.Name, pkg.Version)
		info := &registry.PackageInfo{
			Name:        pkg.Name,
			Version:     pkg.Version,
			DownloadURL: pkg.DownloadURL,
			Checksum:    pkg.Checksum,
		}
		if err := i.Install(info); err != nil {
			return fmt.Errorf("failed to install %s: %w", pkg.Name, err)
		}
		fmt.Printf("  ✓ %s@%s\n", pkg.Name, pkg.Version)
	}

	for _, jar := range jars {
		dep := manifest.JavaDependency{
			GroupID:    jar.GroupID,
			ArtifactID: jar.ArtifactID,
			Version:    jar.Version,
			Checksum:   jar.Checksum,
			URL:        jar.DownloadURL,
		}
		if _, err := os.Stat(filepath.Join(i.jarsDir, dep.JarFileName())); err == nil {
			fmt.Printf("  ✓ %s (already present)\n", jar.Key)
			continue
		}
		fmt.Printf("  → JAR %s\n", jar.Key)
		if err := i.InstallJar(dep); err != nil {
			return fmt.Errorf("failed to install JAR %s: %w", jar.Key, err)
		}
		fmt.Printf("  ✓ %s\n", jar.Key)
	}
	return nil
}

// installFromPlan installs every entry in a freshly resolved Plan.
func (i *Installer) installFromPlan(plan *resolver.Plan) error {
	for _, pkg := range plan.Packages {
		fmt.Printf("  → %s@%s", pkg.Name, pkg.Version.String())
		if len(pkg.RequiredBy) > 0 {
			fmt.Printf("  (required by: %s)", strings.Join(pkg.RequiredBy, ", "))
		}
		fmt.Println()
		info := &registry.PackageInfo{
			Name:        pkg.Name,
			Version:     pkg.Version.String(),
			DownloadURL: pkg.DownloadURL,
			Checksum:    pkg.Checksum,
		}
		if err := i.Install(info); err != nil {
			return fmt.Errorf("failed to install %s: %w", pkg.Name, err)
		}
		fmt.Printf("  ✓ %s@%s\n", pkg.Name, pkg.Version.String())
	}
	for _, dep := range plan.JARs {
		fmt.Printf("  → JAR %s\n", dep.Key())
		if err := i.InstallJar(dep); err != nil {
			return fmt.Errorf("failed to install JAR %s: %w", dep.Key(), err)
		}
		fmt.Printf("  ✓ %s\n", dep.JarFileName())
	}
	return nil
}

// Install downloads, verifies, and unpacks a single BDL package.
func (i *Installer) Install(info *registry.PackageInfo) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "fglpkg-*.zip")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// Download and verify in one streaming pass.
	if err := downloadAndVerify(info.DownloadURL, info.Checksum, info.Name, tmp); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	destDir := filepath.Join(i.packagesDir, info.Name)
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("cannot clean existing package dir: %w", err)
	}
	return extractZip(tmpName, destDir)
}

// InstallJar downloads and verifies a Java JAR into the jars directory.
// The JAR checksum field on JavaDependency is optional; if empty the
// integrity check is skipped (Maven Central is trusted by default).
func (i *Installer) InstallJar(dep manifest.JavaDependency) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	dest := filepath.Join(i.jarsDir, dep.JarFileName())
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("    (already present: %s)\n", dep.JarFileName())
		return nil
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("cannot create jar file: %w", err)
	}

	url := dep.MavenURL()
	fmt.Printf("    Downloading %s\n", url)

	// JavaDependency doesn't carry a checksum field today; pass "" to skip.
	if err := downloadAndVerify(url, dep.Checksum, dep.JarFileName(), f); err != nil {
		f.Close()
		os.Remove(dest)
		return err
	}
	return f.Close()
}

// Remove deletes a BDL package directory.
func (i *Installer) Remove(name string) error {
	dir := filepath.Join(i.packagesDir, name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("package %q is not installed", name)
	}
	return os.RemoveAll(dir)
}

// List returns all currently installed BDL packages by scanning the packages dir.
func (i *Installer) List() ([]InstalledPackage, error) {
	entries, err := os.ReadDir(i.packagesDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var pkgs []InstalledPackage
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		version := "unknown"
		if m, err := manifest.Load(filepath.Join(i.packagesDir, e.Name())); err == nil {
			version = m.Version
		}
		pkgs = append(pkgs, InstalledPackage{Name: e.Name(), Version: version})
	}
	return pkgs, nil
}

// PackagesDir returns the path where BDL packages are installed.
func (i *Installer) PackagesDir() string { return i.packagesDir }

// JarsDir returns the path where Java JARs are stored.
func (i *Installer) JarsDir() string { return i.jarsDir }

// ─── Download + verify ────────────────────────────────────────────────────────

// downloadAndVerify fetches url, streams the body through a DigestingReader
// into w, and verifies the SHA256 against expectedChecksum in a single pass.
// name is used only in error messages.
func downloadAndVerify(url, expectedChecksum, name string, w io.Writer) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed for %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading %s from %s", resp.StatusCode, name, url)
	}

	dr := checksum.NewDigestingReader(resp.Body)
	if _, err := io.Copy(w, dr); err != nil {
		return fmt.Errorf("error writing %s: %w", name, err)
	}

	// Verify after the full body has been streamed — no second read.
	if err := dr.Verify(name, expectedChecksum); err != nil {
		return err // already a descriptive *checksum.ErrMismatch
	}
	return nil
}

// ─── Zip extraction ───────────────────────────────────────────────────────────

func (i *Installer) ensureDirs() error {
	for _, d := range []string{i.packagesDir, i.jarsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", d, err)
		}
	}
	return nil
}

// extractZip unpacks a zip archive into destDir, sanitising all paths.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("cannot open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	for _, f := range r.File {
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}

		// Strip the top-level wrapper directory many zip tools add.
		// e.g. "mypackage-1.0.0/foo.42m" → "foo.42m"
		parts := strings.SplitN(cleanName, string(filepath.Separator), 2)
		if len(parts) == 2 {
			cleanName = parts[1]
		}
		if cleanName == "" {
			continue
		}

		target := filepath.Join(destDir, cleanName)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
