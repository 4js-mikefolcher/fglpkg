// Package server — blob.go defines the BlobStore interface and its two
// implementations: localBlobStore (flat files on disk, for dev / self-hosted)
// and r2BlobStore (Cloudflare R2 via the S3-compatible API, for production).
//
// The BlobStore owns zip artifact storage only. JSON metadata files always
// stay on the local filesystem regardless of which BlobStore is active.
package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BlobStore is the interface for storing and retrieving zip artifacts.
// Implementations must be safe for concurrent use.
type BlobStore interface {
	// Put streams r into the store under key and returns the public URL
	// that clients should use to download the artifact.
	Put(key string, r io.Reader) (publicURL string, err error)

	// PublicURL returns the public download URL for an existing key without
	// re-uploading. Used when rebuilding DownloadURL fields from stored metadata.
	PublicURL(key string) string

	// Delete removes the artifact for key. Best-effort — non-fatal.
	Delete(key string) error
}

// blobKey returns the canonical storage key for a package zip.
// e.g. "myutils/1.2.3.zip"
func blobKey(name, version string) string {
	return name + "/" + version + ".zip"
}

// ─── localBlobStore ───────────────────────────────────────────────────────────

// localBlobStore writes zips to a subdirectory of the registry's data
// directory and serves them through the registry's own /download route.
// Used for local development and self-hosted deployments without R2.
type localBlobStore struct {
	packagesDir string // <dataDir>/packages
	baseURL     string // public base URL of the registry server
}

func newLocalBlobStore(dataDir, baseURL string) *localBlobStore {
	return &localBlobStore{
		packagesDir: filepath.Join(dataDir, "packages"),
		baseURL:     strings.TrimRight(baseURL, "/"),
	}
}

func (s *localBlobStore) Put(key string, r io.Reader) (string, error) {
	dest := filepath.Join(s.packagesDir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("cannot create directory for %s: %w", key, err)
	}

	// Write via temp-file rename for atomicity.
	tmp, err := os.CreateTemp(filepath.Dir(dest), "upload-*.zip.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("cannot write %s: %w", key, err)
	}
	tmp.Close()

	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("cannot finalise %s: %w", key, err)
	}

	return s.PublicURL(key), nil
}

func (s *localBlobStore) PublicURL(key string) string {
	// key is "name/version.zip"; map to /packages/name/version/download
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	name := parts[0]
	version := strings.TrimSuffix(parts[1], ".zip")
	return fmt.Sprintf("%s/packages/%s/%s/download", s.baseURL, name, version)
}

func (s *localBlobStore) Delete(key string) error {
	return os.Remove(filepath.Join(s.packagesDir, filepath.FromSlash(key)))
}

// localPath returns the filesystem path for a given key. Used by
// handleDownload to open the file when localBlobStore is active.
func (s *localBlobStore) localPath(key string) string {
	return filepath.Join(s.packagesDir, filepath.FromSlash(key))
}

// ─── r2BlobStore ──────────────────────────────────────────────────────────────

// R2Config holds the credentials and settings for a Cloudflare R2 bucket.
type R2Config struct {
	AccountID       string // Cloudflare account ID
	AccessKeyID     string // R2 API token key ID
	AccessKeySecret string // R2 API token secret
	BucketName      string // R2 bucket name, e.g. "fglpkg-packages"
	PublicBucketURL string // Public bucket URL, e.g. "https://packages.registry.example.com"
}

// r2BlobStore uploads zips to a Cloudflare R2 public bucket.
// Downloads bypass the registry server entirely — clients fetch directly
// from the Cloudflare CDN using the public bucket URL.
type r2BlobStore struct {
	cfg    R2Config
	client *s3.Client
}

// newR2BlobStore creates an r2BlobStore using the S3-compatible R2 endpoint.
func newR2BlobStore(cfg R2Config) (*r2BlobStore, error) {
	if cfg.AccountID == "" || cfg.AccessKeyID == "" ||
		cfg.AccessKeySecret == "" || cfg.BucketName == "" {
		return nil, fmt.Errorf("R2 config is incomplete: AccountID, AccessKeyID, " +
			"AccessKeySecret and BucketName are all required")
	}

	r2Endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)

	awsCfg, err := config.LoadDefaultConfig(
		nil, // context.Background() equivalent with no ctx
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.AccessKeySecret,
			"",
		)),
		config.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("cannot configure R2 client: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(r2Endpoint)
		o.UsePathStyle = true // required for R2
	})

	return &r2BlobStore{cfg: cfg, client: client}, nil
}

func (s *r2BlobStore) Put(key string, r io.Reader) (string, error) {
	ctx := &httpContext{} // lightweight context for the S3 call

	// The S3 PutObject API requires a known content length for streaming
	// uploads. We buffer to a temp file first to get the size, then stream
	// from disk to R2. This avoids loading the whole zip into memory.
	tmp, err := os.CreateTemp("", "r2-upload-*.zip")
	if err != nil {
		return "", fmt.Errorf("cannot create temp file for R2 upload: %w", err)
	}
	defer os.Remove(tmp.Name())

	size, err := io.Copy(tmp, r)
	if err != nil {
		tmp.Close()
		return "", fmt.Errorf("cannot buffer zip for R2 upload: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return "", err
	}

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.cfg.BucketName),
		Key:           aws.String(key),
		Body:          tmp,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String("application/zip"),
		// Public bucket — no ACL needed; access is controlled at bucket level.
	})
	tmp.Close()
	if err != nil {
		return "", fmt.Errorf("R2 PutObject failed for %s: %w", key, err)
	}

	return s.PublicURL(key), nil
}

func (s *r2BlobStore) PublicURL(key string) string {
	base := strings.TrimRight(s.cfg.PublicBucketURL, "/")
	return fmt.Sprintf("%s/%s", base, key)
}

func (s *r2BlobStore) Delete(key string) error {
	ctx := &httpContext{}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.BucketName),
		Key:    aws.String(key),
	})
	return err
}

// ─── httpContext — minimal context.Context for AWS SDK calls ─────────────────

// httpContext is a minimal context.Context that satisfies the interface
// without importing "context" at package level (keeps the dependency surface
// clear). In production code you'd pass a real context from the HTTP request.
type httpContext struct{}

func (c *httpContext) Deadline() (time.Time, bool)       { return time.Time{}, false }
func (c *httpContext) Done() <-chan struct{}              { return nil }
func (c *httpContext) Err() error                        { return nil }
func (c *httpContext) Value(key any) any                 { return nil }

// ─── BlobStore construction from Config ───────────────────────────────────────

// newBlobStore returns the appropriate BlobStore based on Config.
// If R2 credentials are present, r2BlobStore is returned; otherwise
// localBlobStore is used, which is suitable for development and
// self-hosted deployments.
func newBlobStore(cfg Config) (BlobStore, error) {
	if cfg.R2.AccountID != "" {
		store, err := newR2BlobStore(cfg.R2)
		if err != nil {
			return nil, err
		}
		return store, nil
	}
	return newLocalBlobStore(cfg.DataDir, cfg.BaseURL), nil
}

// isLocal reports whether bs is a localBlobStore. Used by handleDownload to
// decide whether to stream locally or redirect to the CDN URL.
func isLocal(bs BlobStore) (*localBlobStore, bool) {
	l, ok := bs.(*localBlobStore)
	return l, ok
}

// ─── HTTP redirect helper for R2 downloads ───────────────────────────────────

// redirectToBlob issues a 302 redirect to the blob's public URL.
// For local blob stores the download is served inline; this is only called
// for R2 where the file lives on the CDN.
func redirectToBlob(w http.ResponseWriter, r *http.Request, url string) {
	http.Redirect(w, r, url, http.StatusFound)
}
