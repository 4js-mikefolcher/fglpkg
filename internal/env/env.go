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
func (g *Generator) Generate() ([]string, error) {
	var lines []string

	fglldpath, err := g.buildFGLLDPATH()
	if err != nil {
		return nil, err
	}
	lines = append(lines, g.exportLine("FGLLDPATH", fglldpath))

	javaClasspath, err := g.buildJavaClasspath()
	if err != nil {
		return nil, err
	}
	if javaClasspath != "" {
		lines = append(lines, g.exportLine("FGLJAVAPROPERTY_java.class.path", javaClasspath))
	}

	return lines, nil
}

// buildFGLLDPATH constructs the FGLLDPATH value.
// Order of precedence (highest first):
//  1. Workspace member source directories (local dev, no install needed)
//  2. fglpkg installed packages directory
//  3. Pre-existing FGLLDPATH entries
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

	// 2. fglpkg installed packages directory.
	add(g.packagesDir)

	// 3. Preserve existing FGLLDPATH entries.
	if existing := os.Getenv("FGLLDPATH"); existing != "" {
		for _, p := range strings.Split(existing, sep) {
			add(p)
		}
	}

	return strings.Join(parts, sep), nil
}

// buildJavaClasspath constructs FGLJAVAPROPERTY_java.class.path by scanning
// the jars directory for all .jar files, then appending any pre-existing value.
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

	add := func(p string) {
		if p != "" && !seen[p] {
			jars = append(jars, p)
			seen[p] = true
		}
	}

	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
			add(filepath.Join(g.jarsDir, e.Name()))
		}
	}

	envKey := "FGLJAVAPROPERTY_java.class.path"
	if existing := os.Getenv(envKey); existing != "" {
		for _, p := range strings.Split(existing, sep) {
			add(p)
		}
	}

	return strings.Join(jars, sep), nil
}

func (g *Generator) exportLine(key, value string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("SET %s=%s", key, value)
	}
	return fmt.Sprintf("export %s=%s", key, value)
}

func pathSeparator() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}
