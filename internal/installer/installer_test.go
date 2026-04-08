package installer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

func TestMakeBinScriptsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not applicable on Windows")
	}

	pkgDir := t.TempDir()

	// Create the script file.
	scriptDir := filepath.Join(pkgDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptDir, "run.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := &manifest.Manifest{
		Name:    "testpkg",
		Version: "1.0.0",
		Bin:     map[string]string{"run": "scripts/run.sh"},
	}

	if err := makeBinScriptsExecutable(pkgDir, m); err != nil {
		t.Fatalf("makeBinScriptsExecutable: %v", err)
	}

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode()
	if mode&0111 == 0 {
		t.Errorf("expected executable bits set, got mode %o", mode)
	}
}

func TestMakeBinScriptsExecutableMissingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not applicable on Windows")
	}

	pkgDir := t.TempDir()

	m := &manifest.Manifest{
		Name:    "testpkg",
		Version: "1.0.0",
		Bin:     map[string]string{"missing": "scripts/missing.sh"},
	}

	err := makeBinScriptsExecutable(pkgDir, m)
	if err == nil {
		t.Fatal("expected error for missing script file")
	}
}

func TestMakeBinScriptsExecutableMultiple(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not applicable on Windows")
	}

	pkgDir := t.TempDir()
	scriptDir := filepath.Join(pkgDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}

	scripts := []string{"a.sh", "b.py"}
	for _, name := range scripts {
		if err := os.WriteFile(filepath.Join(scriptDir, name), []byte("#!/bin/bash\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	m := &manifest.Manifest{
		Name:    "testpkg",
		Version: "1.0.0",
		Bin: map[string]string{
			"cmd-a": "scripts/a.sh",
			"cmd-b": "scripts/b.py",
		},
	}

	if err := makeBinScriptsExecutable(pkgDir, m); err != nil {
		t.Fatalf("makeBinScriptsExecutable: %v", err)
	}

	for _, name := range scripts {
		info, err := os.Stat(filepath.Join(scriptDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("%s: expected executable bits set, got mode %o", name, info.Mode())
		}
	}
}
