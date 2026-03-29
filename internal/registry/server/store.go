package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── Data model ───────────────────────────────────────────────────────────────

type packageIndex struct {
	Packages map[string]*indexEntry `json:"packages"`
}

type indexEntry struct {
	Name          string `json:"name"`
	LatestVersion string `json:"latestVersion"`
	Description   string `json:"description"`
	Author        string `json:"author"`
}

type packageMeta struct {
	Name     string           `json:"name"`
	Versions []*versionRecord `json:"versions"`
}

type versionRecord struct {
	Version          string            `json:"version"`
	Description      string            `json:"description"`
	Author           string            `json:"author"`
	License          string            `json:"license,omitempty"`
	GeneroConstraint string            `json:"genero,omitempty"`
	DownloadURL      string            `json:"downloadUrl,omitempty"`
	Checksum         string            `json:"checksum"`
	FGLDeps          map[string]string `json:"fglDeps,omitempty"`
	JavaDeps         []javaDep         `json:"javaDeps,omitempty"`
	PublishedAt      string            `json:"publishedAt"`
}

func (pm *packageMeta) findVersion(version string) *versionRecord {
	for _, v := range pm.Versions {
		if v.Version == version {
			return v
		}
	}
	return nil
}

type searchResult struct {
	Name          string `json:"name"`
	LatestVersion string `json:"latestVersion"`
	Description   string `json:"description"`
	Author        string `json:"author"`
}

// ─── fileStore ────────────────────────────────────────────────────────────────

// fileStore manages JSON metadata on local disk and delegates zip artifact
// storage to a BlobStore (local filesystem or Cloudflare R2).
type fileStore struct {
	dataDir string
	blobs   BlobStore
	mu      sync.RWMutex
	index   *packageIndex
}

func (s *fileStore) init() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.indexPath()); os.IsNotExist(err) {
		s.index = &packageIndex{Packages: make(map[string]*indexEntry)}
		return s.saveIndexLocked()
	}

	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		return fmt.Errorf("cannot read index.json: %w", err)
	}
	var idx packageIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return fmt.Errorf("invalid index.json: %w", err)
	}
	if idx.Packages == nil {
		idx.Packages = make(map[string]*indexEntry)
	}
	s.index = &idx
	return nil
}

// ─── Read operations ──────────────────────────────────────────────────────────

func (s *fileStore) loadPackage(name string) (*packageMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadPackageLocked(name)
}

func (s *fileStore) loadPackageLocked(name string) (*packageMeta, error) {
	data, err := os.ReadFile(s.metaPath(name))
	if err != nil {
		return nil, fmt.Errorf("package %q not found", name)
	}
	var pm packageMeta
	if err := json.Unmarshal(data, &pm); err != nil {
		return nil, fmt.Errorf("corrupt meta for %q: %w", name, err)
	}
	return &pm, nil
}

func (s *fileStore) search(term string) []searchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []searchResult
	for _, entry := range s.index.Packages {
		if strings.Contains(strings.ToLower(entry.Name), term) ||
			strings.Contains(strings.ToLower(entry.Description), term) {
			results = append(results, searchResult{
				Name:          entry.Name,
				LatestVersion: entry.LatestVersion,
				Description:   entry.Description,
				Author:        entry.Author,
			})
		}
	}
	return results
}

// ─── Write operations ─────────────────────────────────────────────────────────

// savePackage streams the zip through a SHA256 hasher, uploads it to the
// BlobStore, then atomically updates meta.json and index.json.
// Returns the computed checksum and the blob's public URL.
func (s *fileStore) savePackage(
	name, version string,
	meta publishRequest,
	zipReader io.Reader,
) (checksum, downloadURL string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure metadata directory exists (blobs manage their own dirs).
	if err := os.MkdirAll(filepath.Dir(s.metaPath(name)), 0755); err != nil {
		return "", "", fmt.Errorf("cannot create metadata dir: %w", err)
	}

	// Tee the reader through SHA256 while uploading to the blob store.
	h := sha256.New()
	tee := io.TeeReader(zipReader, h)

	key := blobKey(name, version)
	publicURL, err := s.blobs.Put(key, tee)
	if err != nil {
		return "", "", fmt.Errorf("blob upload failed: %w", err)
	}
	checksum = hex.EncodeToString(h.Sum(nil))

	// Load or initialise package metadata.
	var pm packageMeta
	if existing, err := s.loadPackageLocked(name); err == nil {
		pm = *existing
	} else {
		pm = packageMeta{Name: name}
	}

	pm.Versions = append(pm.Versions, &versionRecord{
		Version:          version,
		Description:      meta.Description,
		Author:           meta.Author,
		License:          meta.License,
		GeneroConstraint: meta.GeneroConstraint,
		DownloadURL:      publicURL,
		Checksum:         checksum,
		FGLDeps:          meta.FGLDeps,
		JavaDeps:         meta.JavaDeps,
		PublishedAt:      time.Now().UTC().Format(time.RFC3339),
	})

	// Write meta.json atomically — roll back blob on failure.
	if err := atomicWriteJSON(s.metaPath(name), &pm); err != nil {
		s.blobs.Delete(key) //nolint:errcheck
		return "", "", fmt.Errorf("cannot update package meta: %w", err)
	}

	// Update lightweight global index.
	s.index.Packages[name] = &indexEntry{
		Name:          name,
		LatestVersion: version,
		Description:   meta.Description,
		Author:        meta.Author,
	}
	if err := s.saveIndexLocked(); err != nil {
		return "", "", fmt.Errorf("cannot update index: %w", err)
	}

	return checksum, publicURL, nil
}

// savePackageMetadata stores only the metadata for a version whose zip is
// hosted externally (e.g., GitHub Releases). No blob storage is involved.
func (s *fileStore) savePackageMetadata(name, version string, meta publishRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.metaPath(name)), 0755); err != nil {
		return fmt.Errorf("cannot create metadata dir: %w", err)
	}

	var pm packageMeta
	if existing, err := s.loadPackageLocked(name); err == nil {
		pm = *existing
	} else {
		pm = packageMeta{Name: name}
	}

	pm.Versions = append(pm.Versions, &versionRecord{
		Version:          version,
		Description:      meta.Description,
		Author:           meta.Author,
		License:          meta.License,
		GeneroConstraint: meta.GeneroConstraint,
		DownloadURL:      meta.DownloadURL,
		Checksum:         meta.Checksum,
		FGLDeps:          meta.FGLDeps,
		JavaDeps:         meta.JavaDeps,
		PublishedAt:      time.Now().UTC().Format(time.RFC3339),
	})

	if err := atomicWriteJSON(s.metaPath(name), &pm); err != nil {
		return fmt.Errorf("cannot update package meta: %w", err)
	}

	s.index.Packages[name] = &indexEntry{
		Name:          name,
		LatestVersion: version,
		Description:   meta.Description,
		Author:        meta.Author,
	}
	if err := s.saveIndexLocked(); err != nil {
		return fmt.Errorf("cannot update index: %w", err)
	}

	return nil
}

// deleteVersion removes a version's blob and strips its record from meta.json.
// Used for checksum-mismatch rollback.
func (s *fileStore) deleteVersion(name, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.blobs.Delete(blobKey(name, version)) //nolint:errcheck

	pm, err := s.loadPackageLocked(name)
	if err != nil {
		return nil
	}
	filtered := pm.Versions[:0]
	for _, v := range pm.Versions {
		if v.Version != version {
			filtered = append(filtered, v)
		}
	}
	pm.Versions = filtered
	return atomicWriteJSON(s.metaPath(name), pm)
}

// downloadURL returns the public URL for a version's zip, preferring the
// stored URL (set at publish time) and falling back to re-deriving it from
// the BlobStore. This handles registry migrations between storage backends.
func (s *fileStore) downloadURL(name, version string) string {
	pkg, err := s.loadPackage(name)
	if err != nil {
		return s.blobs.PublicURL(blobKey(name, version))
	}
	v := pkg.findVersion(version)
	if v != nil && v.DownloadURL != "" {
		return v.DownloadURL
	}
	return s.blobs.PublicURL(blobKey(name, version))
}

func (s *fileStore) saveIndexLocked() error {
	return atomicWriteJSON(s.indexPath(), s.index)
}

// ─── Path helpers ─────────────────────────────────────────────────────────────

func (s *fileStore) indexPath() string {
	return filepath.Join(s.dataDir, "index.json")
}

func (s *fileStore) metaPath(name string) string {
	return filepath.Join(s.dataDir, "packages", name, "meta.json")
}

// ─── Atomic JSON write ────────────────────────────────────────────────────────

func atomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
