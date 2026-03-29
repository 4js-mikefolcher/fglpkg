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
//  2. Each installed package directory
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

	// 1. Workspace member paths (if we're inside a workspace).
	if wsRoot := workspace.FindRoot("."); wsRoot != "" {
		ws, err := workspace.Load(wsRoot)
		if err == nil {
			for _, entry := range ws.FGLLDPATHEntries() {
				add(entry)
			}
		}
	}

	// 2. Each installed package directory (so Genero can resolve paths like
	// com/fourjs/poiapi/Module.42m relative to the package root).
	if entries, err := os.ReadDir(g.packagesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add(filepath.Join(g.packagesDir, e.Name()))
			}
		}
	}

	return strings.Join(parts, sep), nil
}

// buildJavaClasspath returns the fglpkg-managed CLASSPATH entries by
// scanning the jars directory for all .jar files.  The existing
// CLASSPATH value is preserved at eval time via prependExportLine.
func (g *Generator) buildJavaClasspath() (string, error) {
	entries, err := os.ReadDir(g.jarsDir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot read jars directory: %w", err)
	}

	sep := pathSeparator()
	seen := make(map[string]bool)
	var jars []string

	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
			p := filepath.Join(g.jarsDir, e.Name())
			if !seen[p] {
				jars = append(jars, p)
				seen[p] = true
			}
		}
	}

	return strings.Join(jars, sep), nil
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
