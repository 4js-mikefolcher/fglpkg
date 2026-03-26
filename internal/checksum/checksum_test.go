package checksum_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/checksum"
)

// knownDigest is the SHA256 of the ASCII string "hello fglpkg\n".
// Computed with: echo "hello fglpkg" | sha256sum
const (
	testContent    = "hello fglpkg\n"
	knownDigest    = "d1a86e8f023de8d5a80efaa4a9c0a867e2b6b9ef12499b9af7a2f4c5e68b0d23"
	wrongDigest    = "0000000000000000000000000000000000000000000000000000000000000000"
)

// realDigest computes the actual SHA256 of testContent so tests don't rely on
// a hardcoded value that might be wrong.
func realDigest(t *testing.T) string {
	t.Helper()
	d, err := checksum.DigestReader(strings.NewReader(testContent))
	if err != nil {
		t.Fatalf("DigestReader: %v", err)
	}
	return d
}

// ─── DigestReader ─────────────────────────────────────────────────────────────

func TestDigestReader(t *testing.T) {
	d, err := checksum.DigestReader(strings.NewReader(testContent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d) != 64 {
		t.Errorf("expected 64-char hex digest, got %d chars: %s", len(d), d)
	}
	// Deterministic: same input always gives same digest.
	d2, _ := checksum.DigestReader(strings.NewReader(testContent))
	if d != d2 {
		t.Errorf("digest not deterministic: %s vs %s", d, d2)
	}
}

func TestDigestReaderDifferentInputs(t *testing.T) {
	d1, _ := checksum.DigestReader(strings.NewReader("aaa"))
	d2, _ := checksum.DigestReader(strings.NewReader("bbb"))
	if d1 == d2 {
		t.Error("different inputs produced the same digest")
	}
}

// ─── DigestFile ──────────────────────────────────────────────────────────────

func TestDigestFile(t *testing.T) {
	f := writeTempFile(t, testContent)
	got, err := checksum.DigestFile(f)
	if err != nil {
		t.Fatalf("DigestFile: %v", err)
	}
	want := realDigest(t)
	if got != want {
		t.Errorf("DigestFile = %s, want %s", got, want)
	}
}

func TestDigestFileMissing(t *testing.T) {
	_, err := checksum.DigestFile("/nonexistent/path/file.zip")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

// ─── VerifyFile ───────────────────────────────────────────────────────────────

func TestVerifyFileMatch(t *testing.T) {
	f := writeTempFile(t, testContent)
	want := realDigest(t)
	if err := checksum.VerifyFile(f, want); err != nil {
		t.Errorf("expected no error on match, got: %v", err)
	}
}

func TestVerifyFileMismatch(t *testing.T) {
	f := writeTempFile(t, testContent)
	err := checksum.VerifyFile(f, wrongDigest)
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	var mm *checksum.ErrMismatch
	if !errors.As(err, &mm) {
		t.Fatalf("expected *ErrMismatch, got %T: %v", err, err)
	}
	if mm.Expected != wrongDigest {
		t.Errorf("ErrMismatch.Expected = %q, want %q", mm.Expected, wrongDigest)
	}
	if mm.Got == "" {
		t.Error("ErrMismatch.Got should not be empty")
	}
}

func TestVerifyFileSkipsEmptyExpected(t *testing.T) {
	f := writeTempFile(t, testContent)
	// Empty expected → no check, always passes.
	if err := checksum.VerifyFile(f, ""); err != nil {
		t.Errorf("expected no error for empty expected, got: %v", err)
	}
}

func TestVerifyFileUppercaseExpected(t *testing.T) {
	f := writeTempFile(t, testContent)
	want := strings.ToUpper(realDigest(t)) // registry might return uppercase
	if err := checksum.VerifyFile(f, want); err != nil {
		t.Errorf("expected match with uppercase digest, got: %v", err)
	}
}

// ─── DigestingReader ─────────────────────────────────────────────────────────

func TestDigestingReaderMatch(t *testing.T) {
	want := realDigest(t)
	dr := checksum.NewDigestingReader(strings.NewReader(testContent))

	// Simulate writing to a file (drain the reader).
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(dr); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}

	// Content passes through unchanged.
	if buf.String() != testContent {
		t.Errorf("content mismatch: got %q, want %q", buf.String(), testContent)
	}

	if err := dr.Verify("test.zip", want); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestDigestingReaderMismatch(t *testing.T) {
	dr := checksum.NewDigestingReader(strings.NewReader(testContent))
	var buf bytes.Buffer
	buf.ReadFrom(dr) //nolint:errcheck

	err := dr.Verify("test.zip", wrongDigest)
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	var mm *checksum.ErrMismatch
	if !errors.As(err, &mm) {
		t.Fatalf("expected *ErrMismatch, got %T", err)
	}
	if mm.File != "test.zip" {
		t.Errorf("ErrMismatch.File = %q, want %q", mm.File, "test.zip")
	}
}

func TestDigestingReaderSkipsEmptyExpected(t *testing.T) {
	dr := checksum.NewDigestingReader(strings.NewReader(testContent))
	var buf bytes.Buffer
	buf.ReadFrom(dr) //nolint:errcheck

	if err := dr.Verify("test.zip", ""); err != nil {
		t.Errorf("expected no error for empty expected, got: %v", err)
	}
}

func TestDigestingReaderPartialRead(t *testing.T) {
	// Digest should reflect only the bytes actually read, not the full stream.
	content := "abcdefgh"
	dr := checksum.NewDigestingReader(strings.NewReader(content))

	half := make([]byte, 4)
	dr.Read(half) //nolint:errcheck

	partialDigest := dr.Digest()
	fullDigest, _ := checksum.DigestReader(strings.NewReader(content))

	if partialDigest == fullDigest {
		t.Error("partial read digest should differ from full content digest")
	}

	want, _ := checksum.DigestReader(strings.NewReader("abcd"))
	if partialDigest != want {
		t.Errorf("partial digest = %s, want %s", partialDigest, want)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "fglpkg-checksum-*.tmp")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	return f.Name()
}
