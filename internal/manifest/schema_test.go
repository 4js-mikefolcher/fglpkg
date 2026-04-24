package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestSchemaCoversAllManifestFields is a drift detector: every JSON tag
// name on manifest.Manifest should appear in the top-level schema
// properties. If someone adds a field to the Go struct without updating
// schema/fglpkg.schema.json, this test fails and names the missing key.
func TestSchemaCoversAllManifestFields(t *testing.T) {
	schemaPath := findSchemaPath(t)
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if len(schema.Properties) == 0 {
		t.Fatal("schema has no properties")
	}

	// Collect JSON tags from the Manifest struct.
	typ := reflect.TypeOf(manifest.Manifest{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("manifest field %q has no entry in %s", name, filepath.Base(schemaPath))
		}
	}
}

// findSchemaPath walks up from the test file to the repo root.
func findSchemaPath(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	dir := filepath.Dir(here)
	for i := 0; i < 5; i++ {
		candidate := filepath.Join(dir, "schema", "fglpkg.schema.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate schema/fglpkg.schema.json from test file")
	return ""
}
