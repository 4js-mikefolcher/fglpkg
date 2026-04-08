package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

const Filename = "fglpkg.json"

// Manifest represents the fglpkg.json file for a package or project.
type Manifest struct {
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Description      string            `json:"description,omitempty"`
	Author           string            `json:"author,omitempty"`
	License          string            `json:"license,omitempty"`
	Repository       string            `json:"repository,omitempty"`
	Main             string            `json:"main,omitempty"` // primary .42m entry point
	// GeneroConstraint declares which Genero BDL runtime versions this package
	// is compatible with, using standard semver constraint syntax.
	// Examples: "^4.0.0", ">=3.20.0 <5.0.0", "^3.20.0 || ^4.0.0"
	// Omit or set to "*" to indicate compatibility with any version.
	GeneroConstraint string            `json:"genero,omitempty"`
	Dependencies     Dependencies      `json:"dependencies"`
	Root             string            `json:"root,omitempty"`  // base directory for package files (default ".")
	Files            []string          `json:"files,omitempty"` // glob patterns for package zip
	Bin              map[string]string `json:"bin,omitempty"`   // command name -> script path
	Docs             []string          `json:"docs,omitempty"`  // glob patterns for doc files
	Scripts          map[string]string `json:"scripts,omitempty"`
}

// Dependencies holds both FGL and Java dependency declarations.
type Dependencies struct {
	FGL  map[string]string    `json:"fgl,omitempty"`  // name -> version constraint
	Java []JavaDependency      `json:"java,omitempty"` // Maven coordinates
}

// JavaDependency describes a Java JAR dependency using Maven coordinates.
type JavaDependency struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
	// Checksum is the expected SHA256 hex digest of the JAR file.
	// If provided, the downloaded JAR is verified before use.
	// If omitted, the integrity check is skipped (Maven Central is trusted).
	Checksum   string `json:"checksum,omitempty"`
	// Optional: if omitted, derived from groupId/artifactId/version automatically.
	JarFile    string `json:"jar,omitempty"`
	// Optional: override the download URL entirely.
	URL        string `json:"url,omitempty"`
}

// MavenURL returns the Maven Central download URL for this JAR.
func (j JavaDependency) MavenURL() string {
	if j.URL != "" {
		return j.URL
	}
	// Convert groupId dots to slashes for the URL path
	groupPath := ""
	for _, c := range j.GroupID {
		if c == '.' {
			groupPath += "/"
		} else {
			groupPath += string(c)
		}
	}
	jar := j.JarFile
	if jar == "" {
		jar = fmt.Sprintf("%s-%s.jar", j.ArtifactID, j.Version)
	}
	return fmt.Sprintf(
		"https://repo1.maven.org/maven2/%s/%s/%s/%s",
		groupPath, j.ArtifactID, j.Version, jar,
	)
}

// JarFileName returns the local filename to use when saving this JAR.
func (j JavaDependency) JarFileName() string {
	if j.JarFile != "" {
		return j.JarFile
	}
	return fmt.Sprintf("%s-%s.jar", j.ArtifactID, j.Version)
}

// Key returns a unique string key for this Java dep (groupId:artifactId).
func (j JavaDependency) Key() string {
	return j.GroupID + ":" + j.ArtifactID
}

// BinFiles returns the deduplicated script file paths from the bin map,
// sorted for deterministic ordering.
func (m *Manifest) BinFiles() []string {
	if len(m.Bin) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var paths []string
	for _, p := range m.Bin {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

// New creates a new Manifest with sensible defaults.
func New(name, version, description, author string) *Manifest {
	return &Manifest{
		Name:        name,
		Version:     version,
		Description: description,
		Author:      author,
		License:     "UNLICENSED",
		Dependencies: Dependencies{
			FGL:  map[string]string{},
			Java: []JavaDependency{},
		},
	}
}

// Load reads and parses fglpkg.json from dir.
func Load(dir string) (*Manifest, error) {
	path := filepath.Join(dir, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", Filename, err)
	}
	if m.Dependencies.FGL == nil {
		m.Dependencies.FGL = map[string]string{}
	}
	return &m, nil
}

// LoadOrNew loads fglpkg.json if it exists, otherwise returns a blank manifest.
func LoadOrNew(dir string) (*Manifest, error) {
	m, err := Load(dir)
	if os.IsNotExist(err) {
		return New(filepath.Base(dir), "0.1.0", "", ""), nil
	}
	return m, err
}

// Save writes the manifest as formatted JSON to dir/fglpkg.json.
func (m *Manifest) Save(dir string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, Filename)
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// AddFGLDependency adds or updates a BDL package dependency.
func (m *Manifest) AddFGLDependency(name, version string) {
	if m.Dependencies.FGL == nil {
		m.Dependencies.FGL = map[string]string{}
	}
	m.Dependencies.FGL[name] = version
}

// RemoveFGLDependency removes a BDL package dependency.
func (m *Manifest) RemoveFGLDependency(name string) {
	delete(m.Dependencies.FGL, name)
}

// AddJavaDependency adds or replaces a Java dependency by groupId:artifactId key.
func (m *Manifest) AddJavaDependency(dep JavaDependency) {
	for i, existing := range m.Dependencies.Java {
		if existing.Key() == dep.Key() {
			m.Dependencies.Java[i] = dep
			return
		}
	}
	m.Dependencies.Java = append(m.Dependencies.Java, dep)
}

// RemoveJavaDependency removes a Java dependency by groupId:artifactId key.
func (m *Manifest) RemoveJavaDependency(key string) {
	filtered := m.Dependencies.Java[:0]
	for _, dep := range m.Dependencies.Java {
		if dep.Key() != key {
			filtered = append(filtered, dep)
		}
	}
	m.Dependencies.Java = filtered
}

// Validate performs basic sanity checks on the manifest.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest missing required field: name")
	}
	if m.Version == "" {
		return fmt.Errorf("manifest missing required field: version")
	}
	if m.GeneroConstraint != "" && m.GeneroConstraint != "*" {
		if _, err := semver.ParseConstraint(m.GeneroConstraint); err != nil {
			return fmt.Errorf("invalid genero constraint %q: %w", m.GeneroConstraint, err)
		}
	}
	for _, dep := range m.Dependencies.Java {
		if dep.GroupID == "" || dep.ArtifactID == "" || dep.Version == "" {
			return fmt.Errorf(
				"java dependency missing required fields (groupId, artifactId, version): %+v", dep,
			)
		}
	}
	for cmd, scriptPath := range m.Bin {
		if cmd == "" {
			return fmt.Errorf("bin command name must not be empty")
		}
		if strings.ContainsAny(cmd, "/\\") {
			return fmt.Errorf("bin command name %q must not contain path separators", cmd)
		}
		if scriptPath == "" {
			return fmt.Errorf("bin script path for command %q must not be empty", cmd)
		}
		if filepath.IsAbs(scriptPath) {
			return fmt.Errorf("bin script path %q for command %q must be relative", scriptPath, cmd)
		}
	}
	for _, pattern := range m.Docs {
		// Strip doublestar segments for validation since filepath.Match
		// doesn't support "**", but the rest of the pattern must be valid.
		cleaned := strings.ReplaceAll(pattern, "**", "star")
		if _, err := filepath.Match(cleaned, "test"); err != nil {
			return fmt.Errorf("invalid docs glob pattern %q: %w", pattern, err)
		}
	}
	return nil
}
