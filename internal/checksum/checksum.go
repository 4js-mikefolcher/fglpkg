// Package checksum provides SHA256 verification for downloaded files.
//
// All downloaded artifacts (BDL package zips and Java JARs) are verified
// against a hex-encoded SHA256 digest supplied by the registry before any
// extraction or use occurs. A mismatch causes an immediate, descriptive error.
package checksum

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

// ErrMismatch is returned when a file's digest does not match the expected one.
type ErrMismatch struct {
	File     string
	Expected string
	Got      string
}

func (e *ErrMismatch) Error() string {
	return fmt.Sprintf(
		"checksum mismatch for %q:\n  expected: %s\n  got:      %s\n"+
			"The file may be corrupted or tampered with. "+
			"Delete it and retry, or contact the package author.",
		e.File, e.Expected, e.Got,
	)
}

// VerifyFile computes the SHA256 digest of the file at path and compares it
// to the hex-encoded expected string. Returns *ErrMismatch on failure.
// If expected is empty the check is skipped (registry entry has no checksum).
func VerifyFile(path, expected string) error {
	if expected == "" {
		return nil
	}
	expected = normalise(expected)

	got, err := DigestFile(path)
	if err != nil {
		return fmt.Errorf("cannot compute checksum for %s: %w", path, err)
	}
	if got != expected {
		return &ErrMismatch{File: path, Expected: expected, Got: got}
	}
	return nil
}

// DigestFile computes and returns the lowercase hex SHA256 of the file at path,
// streaming to avoid loading the whole file into memory.
func DigestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return DigestReader(f)
}

// DigestReader computes and returns the lowercase hex SHA256 of all bytes in r.
func DigestReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ─── DigestingReader ─────────────────────────────────────────────────────────

// DigestingReader wraps an io.Reader and accumulates a SHA256 digest as data
// flows through it. This enables a single streaming pass that simultaneously
// writes a file to disk and computes its checksum — no second read needed.
//
// Usage:
//
//	dr := checksum.NewDigestingReader(httpBody)
//	io.Copy(destFile, dr)
//	if err := dr.Verify("mypackage.zip", expectedHex); err != nil { ... }
type DigestingReader struct {
	src io.Reader
	h   hash.Hash
	tee io.Reader
}

// NewDigestingReader wraps r with a SHA256 hasher.
func NewDigestingReader(r io.Reader) *DigestingReader {
	h := sha256.New()
	return &DigestingReader{src: r, h: h, tee: io.TeeReader(r, h)}
}

// Read implements io.Reader, feeding each byte through the SHA256 hasher.
func (d *DigestingReader) Read(p []byte) (int, error) {
	return d.tee.Read(p)
}

// Digest returns the lowercase hex SHA256 of all bytes read so far.
func (d *DigestingReader) Digest() string {
	return hex.EncodeToString(d.h.Sum(nil))
}

// Verify compares the accumulated digest against expected.
// Returns *ErrMismatch if they differ, nil if they match or expected is empty.
func (d *DigestingReader) Verify(name, expected string) error {
	if expected == "" {
		return nil
	}
	expected = normalise(expected)
	got := d.Digest()
	if got != expected {
		return &ErrMismatch{File: name, Expected: expected, Got: got}
	}
	return nil
}

// normalise lowercases and trims whitespace from a hex digest string.
func normalise(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
