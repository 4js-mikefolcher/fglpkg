package credentials_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
)

const (
	registryURL = "https://registry.fglpkg.dev"
	testToken   = "abc123def456"
)

func TestLoadMissing(t *testing.T) {
	f, err := credentials.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(f.Registries) != 0 {
		t.Errorf("expected empty registries, got %d", len(f.Registries))
	}
}

func TestSetAndGet(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")

	e, ok := f.Get(registryURL)
	if !ok {
		t.Fatal("Get returned false after Set")
	}
	if e.Token != testToken {
		t.Errorf("token = %q, want %q", e.Token, testToken)
	}
	if e.Username != "alice" {
		t.Errorf("username = %q, want %q", e.Username, "alice")
	}
	if e.SavedAt == "" {
		t.Error("SavedAt should not be empty")
	}
}

func TestSaveAndLoad(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")
	if err := f.Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f2, err := credentials.Load(home)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	e, ok := f2.Get(registryURL)
	if !ok {
		t.Fatal("Get returned false after save/load round-trip")
	}
	if e.Token != testToken {
		t.Errorf("token = %q, want %q", e.Token, testToken)
	}
}

func TestFilePermissions(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")
	f.Save(home) //nolint:errcheck

	info, err := os.Stat(filepath.Join(home, "credentials.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
}

func TestDelete(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")
	f.Delete(registryURL)

	_, ok := f.Get(registryURL)
	if ok {
		t.Error("Get should return false after Delete")
	}
}

func TestURLNormalisation(t *testing.T) {
	home := t.TempDir()
	f, _ := credentials.Load(home)

	// Set with trailing slash + uppercase.
	f.Set("https://Registry.Example.com/", testToken, "bob")

	// Get without trailing slash + lowercase — should still find it.
	_, ok := f.Get("https://registry.example.com")
	if !ok {
		t.Error("URL normalisation failed: Get with different casing/trailing slash returned false")
	}
}

func TestTokenForEnvVarOverride(t *testing.T) {
	t.Setenv("FGLPKG_PUBLISH_TOKEN", "env-token")
	tok := credentials.TokenFor(t.TempDir(), registryURL)
	if tok != "env-token" {
		t.Errorf("TokenFor = %q, want env-token", tok)
	}
}

func TestTokenForCredentialsFile(t *testing.T) {
	os.Unsetenv("FGLPKG_PUBLISH_TOKEN")
	home := t.TempDir()
	f, _ := credentials.Load(home)
	f.Set(registryURL, testToken, "alice")
	f.Save(home) //nolint:errcheck

	tok := credentials.TokenFor(home, registryURL)
	if tok != testToken {
		t.Errorf("TokenFor = %q, want %q", tok, testToken)
	}
}

func TestTokenForNotFound(t *testing.T) {
	os.Unsetenv("FGLPKG_PUBLISH_TOKEN")
	tok := credentials.TokenFor(t.TempDir(), registryURL)
	if tok != "" {
		t.Errorf("TokenFor = %q, want empty", tok)
	}
}
