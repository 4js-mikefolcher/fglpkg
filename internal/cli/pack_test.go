package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestBuildPackageZipContents verifies the zip builder includes the
// expected files for a representative manifest (default patterns + docs
// + bin). buildPackageZip reads from the current working directory, so
// the test Chdirs into a temp project.
func TestBuildPackageZipContents(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "packtest",
  "version": "1.0.0",
  "dependencies": { "fgl": {} },
  "docs": ["README.md"],
  "bin": { "migrate": "scripts/migrate.sh" }
}`)
	write("Main.42m", "MAIN\nEND MAIN\n")
	write("pkg/Util.42m", "FUNCTION helper() END FUNCTION\n")
	write("README.md", "# Packtest\n")
	write("scripts/migrate.sh", "#!/bin/sh\necho migrate\n")
	// A file that should be excluded (not matching any pattern).
	write("notes.txt", "scratch notes\n")

	// buildPackageZip walks the current directory, so swap cwd.
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, sum, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("zip is empty")
	}
	if len(sum) != 64 {
		t.Errorf("SHA256 hex digest should be 64 chars, got %d", len(sum))
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	got := map[string]bool{}
	for _, f := range r.File {
		got[f.Name] = true
	}

	wantIncluded := []string{
		"fglpkg.json",
		"Main.42m",
		"pkg/Util.42m",
		"README.md",
		"scripts/migrate.sh",
	}
	for _, name := range wantIncluded {
		if !got[name] {
			t.Errorf("expected %q in zip, got entries: %v", name, keys(got))
		}
	}
	if got["notes.txt"] {
		t.Errorf("notes.txt should not be in zip (no matching pattern)")
	}
}

func TestListZipEntriesSortedAndSized(t *testing.T) {
	// Build a tiny zip in-memory.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	mustWrite := func(name, body string) {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}
	mustWrite("zeta.txt", "zz")
	mustWrite("alpha.txt", "aaa")
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := listZipEntries(buf.Bytes())
	if err != nil {
		t.Fatalf("listZipEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].name != "alpha.txt" || entries[1].name != "zeta.txt" {
		t.Errorf("entries not sorted: %+v", entries)
	}
	if entries[0].size != 3 || entries[1].size != 2 {
		t.Errorf("sizes wrong: %+v", entries)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
