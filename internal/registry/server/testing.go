package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
)

// NewTestServer creates an http.Handler backed by a real fileStore and
// authStore rooted at cfg.DataDir, without starting a TCP listener.
// Exported so _test packages can spin up an in-process registry via
// httptest.NewServer.
func NewTestServer(cfg Config) (http.Handler, error) {
	if err := os.MkdirAll(fmt.Sprintf("%s/packages", cfg.DataDir), 0755); err != nil {
		return nil, fmt.Errorf("cannot create data directory: %w", err)
	}
	h, err := newHandler(cfg)
	if err != nil {
		return nil, err
	}
	return withLogging(buildMux(h)), nil
}

// newTestServerWithConfig is a convenience wrapper used by _test packages
// to create a live *httptest.Server with arbitrary config options.
// It is placed here (not in testing_test.go) so both server_test.go and
// auth_test.go can call it without duplication.
func newTestServerWithConfig(cfg Config) (*httptest.Server, error) {
	h, err := NewTestServer(cfg)
	if err != nil {
		return nil, err
	}
	return httptest.NewServer(h), nil
}
