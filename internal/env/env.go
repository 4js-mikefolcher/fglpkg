package env

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/workspace"
)

// Generator builds the environment variable exports needed for Genero BDL.
type Generator struct {
	home        string
	packagesDir string
	jarsDir     string
}

// New creates a Generator rooted at the fglpkg home directory.
func New(home string) *Generator {
	return &Generator{
		home:        home,
		packagesDir: filepath.Join(home, "packages"),
		jarsDir:     filepath.Join(home, "jars"),
	}
}

// Generate returns a slice of shell export lines suitable for eval.
// On Unix:  export VAR=value
// On Windows: SET VAR=value
//
// The generated exports prepend fglpkg-managed paths to any existing
// value of FGLLDPATH / CLASSPATH so that user or system entries are
// never lost.
func (g *Generator) Generate() ([]string, error) {
	var lines []string

	fglldpath, err := g.buildFGLLDPATH()
	if err != nil {
		return nil, err
	}
	lines = append(lines, g.prependExportLine("FGLLDPATH", fglldpath))

	javaClasspath, err := g.buildJavaClasspath()
	if err != nil {
		return nil, err
	}
	if javaClasspath != "" {
		lines = append(lines, g.prependExportLine("CLASSPATH", javaClasspath))
	}

	return lines, nil
}

// buildFGLLDPATH returns the fglpkg-managed FGLLDPATH entries.
// Order of precedence (highest first):
//  1. Workspace member source directories (local dev, no install needed)
//  2. Local project packages (.fglpkg/packages/ in cwd)
//  3. Global installed packages (~/.fglpkg/packages/)
//
// The existing FGLLDPATH value is preserved at eval time via
// prependExportLine, so we do not read it here.
func (g *Generator) buildFGLLDPATH() (string, error) {
	sep := pathSeparator()
	var parts []string
	seen := make(map[string]bool)

	add := func(p string) {
		if p != "" && !seen[p] {
			parts = append(parts, p)
			seen[p] = true
		}
	}

	addPackagesFrom := func(dir string) {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					add(filepath.Join(dir, e.Name()))
				}
			}
		}
	}

	// 1. Workspace member paths (if we're inside a workspace).
	if wsRoot := workspace.FindRoot("."); wsRoot != "" {
		ws, err := workspace.Load(wsRoot)
		if err == nil {
			for _, entry := range ws.FGLLDPATHEntries() {
				add(entry)
			}
		}
	}

	// 2. Local project packages (higher priority than global).
	localPkgs := filepath.Join(".", ".fglpkg", "packages")
	if abs, err := filepath.Abs(localPkgs); err == nil {
		if abs != g.packagesDir { // avoid duplicating if local == global
			addPackagesFrom(abs)
		}
	}

	// 3. Global installed packages.
	addPackagesFrom(g.packagesDir)

	return strings.Join(parts, sep), nil
}

// buildJavaClasspath returns the fglpkg-managed CLASSPATH entries by
// scanning the jars directory for all .jar files.  Local project jars
// (.fglpkg/jars/) are included with higher priority than global jars.
// The existing CLASSPATH value is preserved at eval time via prependExportLine.
func (g *Generator) buildJavaClasspath() (string, error) {
	sep := pathSeparator()
	seen := make(map[string]bool)
	var jars []string

	addJarsFrom := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
				p := filepath.Join(dir, e.Name())
				if !seen[p] {
					jars = append(jars, p)
					seen[p] = true
				}
			}
		}
	}

	// Local project jars (higher priority).
	localJars := filepath.Join(".", ".fglpkg", "jars")
	if abs, err := filepath.Abs(localJars); err == nil {
		if abs != g.jarsDir {
			addJarsFrom(abs)
		}
	}

	// Global jars.
	addJarsFrom(g.jarsDir)

	return strings.Join(jars, sep), nil
}

// GenerateLocal returns export lines using only the local project's
// .fglpkg/packages and .fglpkg/jars directories.
func (g *Generator) GenerateLocal() ([]string, error) {
	var lines []string

	localPkgs := filepath.Join(".", ".fglpkg", "packages")
	fglldpath, err := g.buildPathsFrom(localPkgs, true)
	if err != nil {
		return nil, err
	}
	if fglldpath != "" {
		lines = append(lines, g.prependExportLine("FGLLDPATH", fglldpath))
	}

	localJars := filepath.Join(".", ".fglpkg", "jars")
	classpath, err := g.buildPathsFrom(localJars, false)
	if err != nil {
		return nil, err
	}
	if classpath != "" {
		lines = append(lines, g.prependExportLine("CLASSPATH", classpath))
	}

	return lines, nil
}

// GenerateGST returns environment variable assignments in Genero Studio
// format. Genero Studio uses:
//   - $(ProjectDir) for the base project directory
//   - $(VARIABLE) to reference environment variables
//   - ; as the path separator (always, regardless of OS)
func (g *Generator) GenerateGST() ([]string, error) {
	var lines []string

	localPkgs := filepath.Join(".", ".fglpkg", "packages")
	fglldpath, err := g.buildGSTPaths(localPkgs, true)
	if err != nil {
		return nil, err
	}
	if fglldpath != "" {
		lines = append(lines, fmt.Sprintf("FGLLDPATH=%s;$(FGLLDPATH)", fglldpath))
	}

	localJars := filepath.Join(".", ".fglpkg", "jars")
	classpath, err := g.buildGSTPaths(localJars, false)
	if err != nil {
		return nil, err
	}
	if classpath != "" {
		lines = append(lines, fmt.Sprintf("CLASSPATH=%s;$(CLASSPATH)", classpath))
	}

	return lines, nil
}

// buildPathsFrom scans a directory and returns paths joined by the OS separator.
// If isDirs is true, it collects subdirectories; otherwise, it collects .jar files.
func (g *Generator) buildPathsFrom(dir string, isDirs bool) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil
	}
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot read directory %s: %w", dir, err)
	}

	sep := pathSeparator()
	var parts []string
	for _, e := range entries {
		if isDirs && e.IsDir() {
			parts = append(parts, filepath.Join(abs, e.Name()))
		} else if !isDirs && !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
			parts = append(parts, filepath.Join(abs, e.Name()))
		}
	}
	return strings.Join(parts, sep), nil
}

// buildGSTPaths scans a directory and returns paths in Genero Studio format,
// using $(ProjectDir) as the base and ; as the separator.
func (g *Generator) buildGSTPaths(dir string, isDirs bool) (string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot read directory %s: %w", dir, err)
	}

	var parts []string
	for _, e := range entries {
		if isDirs && e.IsDir() {
			parts = append(parts, "$(ProjectDir)/.fglpkg/packages/"+e.Name())
		} else if !isDirs && !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
			parts = append(parts, "$(ProjectDir)/.fglpkg/jars/"+e.Name())
		}
	}
	return strings.Join(parts, ";"), nil
}

// BuildFGLLDPATH returns the raw FGLLDPATH value (no export prefix).
// Useful for programmatically setting the environment (e.g., fglpkg bdl).
func (g *Generator) BuildFGLLDPATH() (string, error) {
	return g.buildFGLLDPATH()
}

// BuildJavaClasspath returns the raw CLASSPATH value (no export prefix).
func (g *Generator) BuildJavaClasspath() (string, error) {
	return g.buildJavaClasspath()
}

// MergeEnvVar prepends fglpkgValue to existingValue using the OS path
// separator. Returns just fglpkgValue if existingValue is empty, and
// vice versa.
func MergeEnvVar(fglpkgValue, existingValue string) string {
	if fglpkgValue == "" {
		return existingValue
	}
	if existingValue == "" {
		return fglpkgValue
	}
	return fglpkgValue + pathSeparator() + existingValue
}

// prependExportLine emits a shell statement that prepends value to the
// existing variable, so that user/system entries are never lost.
//
// Unix output:  export VAR='/new/paths'"${VAR:+:$VAR}"
// Windows output: SET VAR=/new/paths;%VAR%
func (g *Generator) prependExportLine(key, value string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("SET %s=%s;%%%s%%", key, value, key)
	}
	// The ${VAR:+:$VAR} construct expands to ":$VAR" only when VAR is
	// non-empty, avoiding a trailing colon when the variable is unset.
	return fmt.Sprintf("export %s=%s\"${%s:+:%s}\"", key, value, key, "$"+key)
}

func pathSeparator() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}
