// Package server implements the fglpkg registry HTTP server.
//
// API surface:
//
//	GET  /health                                liveness probe
//	GET  /search?q=<term>                       search packages
//
//	GET  /packages/:name/versions               list versions + Genero constraints
//	GET  /packages/:name/:version               full package metadata
//	GET  /packages/:name/:version/download      stream the zip
//	POST /packages/:name/:version/publish       publish a new version (auth required)
//	GET  /packages/:name/owners                 list owners
//	POST /packages/:name/owners                 add an owner
//	DELETE /packages/:name/owners/:user         remove an owner
//
//	POST   /auth/token                          create a user + token (admin only)
//	DELETE /auth/token                          revoke a token
//	POST   /auth/token/rotate                   rotate own token
//	GET    /auth/whoami                         identify current token
//	GET    /auth/users                          list all users (admin only)
//
// Read routes (GET /packages/*, GET /search) require authentication only when
// Config.RequireReadAuth is true.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config holds server configuration.
type Config struct {
	Addr            string   // e.g. ":8080"
	DataDir         string   // root of the flat-file metadata store
	PublishToken    string   // admin bootstrap bearer token
	BaseURL         string   // public base URL of this registry server
	RequireReadAuth bool     // if true, GET routes also require a valid token
	R2              R2Config // Cloudflare R2 config; zero value uses local storage
}

// Run starts the HTTP server and blocks until it exits.
func Run(cfg Config) error {
	h, err := newHandler(cfg)
	if err != nil {
		return err
	}

	mux := buildMux(h)
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      withLogging(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return srv.ListenAndServe()
}

// ─── Handler ──────────────────────────────────────────────────────────────────

type handler struct {
	cfg   Config
	store *fileStore
	auth  *authStore
}

func newHandler(cfg Config) (*handler, error) {
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "packages"), 0755); err != nil {
		return nil, fmt.Errorf("cannot create data directory: %w", err)
	}

	blobs, err := newBlobStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot initialise blob store: %w", err)
	}

	store := &fileStore{dataDir: cfg.DataDir, blobs: blobs}
	if err := store.init(); err != nil {
		return nil, fmt.Errorf("cannot initialise store: %w", err)
	}

	auth, err := newAuthStore(cfg.DataDir, cfg.PublishToken)
	if err != nil {
		return nil, fmt.Errorf("cannot initialise auth store: %w", err)
	}

	return &handler{cfg: cfg, store: store, auth: auth}, nil
}

func buildMux(h *handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/search", h.handleSearch)
	mux.HandleFunc("/auth/token/rotate", h.handleTokenRotate)
	mux.HandleFunc("/auth/token", h.handleToken)
	mux.HandleFunc("/auth/whoami", h.handleWhoami)
	mux.HandleFunc("/auth/users", h.handleUsers)
	mux.HandleFunc("/packages/", h.handlePackages)
	return mux
}

// ─── Auth middleware helpers ──────────────────────────────────────────────────

// requireAuth authenticates the request and returns the authResult.
// Writes 401 and returns false on failure.
func (h *handler) requireAuth(w http.ResponseWriter, r *http.Request) (authResult, bool) {
	result, err := h.auth.authenticate(tokenFromRequest(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required: "+err.Error())
		return authResult{}, false
	}
	return result, true
}

// requireAdmin authenticates and checks for admin. Writes 401/403 on failure.
func (h *handler) requireAdmin(w http.ResponseWriter, r *http.Request) (authResult, bool) {
	ar, ok := h.requireAuth(w, r)
	if !ok {
		return authResult{}, false
	}
	if !ar.IsAdmin {
		writeError(w, http.StatusForbidden, "admin token required")
		return authResult{}, false
	}
	return ar, true
}

// optionalAuth authenticates if RequireReadAuth is set. Returns (result, true)
// on success or when auth is not required. Writes 401 and returns false when
// RequireReadAuth is set and auth fails.
func (h *handler) optionalAuth(w http.ResponseWriter, r *http.Request) (authResult, bool) {
	if !h.cfg.RequireReadAuth {
		return authResult{}, true
	}
	return h.requireAuth(w, r)
}

// ─── /auth/token  (POST = create, DELETE = revoke) ───────────────────────────

func (h *handler) handleToken(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleTokenCreate(w, r)
	case http.MethodDelete:
		h.handleTokenRevoke(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "POST or DELETE only")
	}
}

// POST /auth/token  — admin creates a new user + returns their token.
func (h *handler) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	ar, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}

	var req struct {
		Username string `json:"username"`
		Email    string `json:"email,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	rawToken, err := h.auth.createUser(req.Username, req.Email, ar.Username)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"username": req.Username,
		"token":    rawToken,
		"message":  "Store this token securely — it will not be shown again.",
	})
}

// DELETE /auth/token  — revoke a token (own, or another user's if admin).
func (h *handler) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	ar, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	// Optional JSON body: {"username": "target"} — if absent, revoke own token.
	var req struct {
		Username string `json:"username"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	target := req.Username
	if target == "" {
		target = ar.Username
	}

	if err := h.auth.revokeToken(ar.Username, target, ar.IsAdmin); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "token revoked"})
}

// ─── POST /auth/token/rotate ─────────────────────────────────────────────────

func (h *handler) handleTokenRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	ar, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	// Optional body: {"username":"target"} — admin can rotate anyone's token.
	var req struct {
		Username string `json:"username"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	target := req.Username
	if target == "" {
		target = ar.Username
	}

	newToken, err := h.auth.rotateToken(ar.Username, target, ar.IsAdmin)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"username": target,
		"token":    newToken,
		"message":  "Store this token securely — it will not be shown again.",
	})
}

// ─── GET /auth/whoami ─────────────────────────────────────────────────────────

func (h *handler) handleWhoami(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ar, ok := h.requireAuth(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username": ar.Username,
		"isAdmin":  ar.IsAdmin,
	})
}

// ─── GET /auth/users ──────────────────────────────────────────────────────────

func (h *handler) handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{
		"users": h.auth.listUsers(),
	})
}

// ─── /packages/* ──────────────────────────────────────────────────────────────

func (h *handler) handlePackages(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/packages/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")

	switch {
	case len(parts) == 2 && parts[1] == "versions":
		h.handleVersionList(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "owners":
		h.handleOwners(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "owners":
		h.handleOwnerRemove(w, r, parts[0], parts[2])
	case len(parts) == 2:
		h.handlePackageInfo(w, r, parts[0], parts[1])
	case len(parts) == 3 && parts[2] == "download":
		h.handleDownload(w, r, parts[0], parts[1])
	case len(parts) == 3 && parts[2] == "publish":
		h.handlePublish(w, r, parts[0], parts[1])
	default:
		writeError(w, http.StatusNotFound, "unknown route")
	}
}

// ─── GET /packages/:name/versions ─────────────────────────────────────────────

func (h *handler) handleVersionList(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if _, ok := h.optionalAuth(w, r); !ok {
		return
	}

	pkg, err := h.store.loadPackage(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "package not found")
		return
	}

	type versionEntry struct {
		Version          string `json:"version"`
		GeneroConstraint string `json:"genero,omitempty"`
	}
	type response struct {
		Name           string         `json:"name"`
		Versions       []string       `json:"versions"`
		VersionEntries []versionEntry `json:"versionEntries"`
	}

	resp := response{Name: name}
	for _, v := range pkg.Versions {
		resp.Versions = append(resp.Versions, v.Version)
		resp.VersionEntries = append(resp.VersionEntries, versionEntry{
			Version:          v.Version,
			GeneroConstraint: v.GeneroConstraint,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── GET /packages/:name/:version ─────────────────────────────────────────────

func (h *handler) handlePackageInfo(w http.ResponseWriter, r *http.Request, name, version string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if _, ok := h.optionalAuth(w, r); !ok {
		return
	}

	pkg, err := h.store.loadPackage(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "package not found")
		return
	}
	ver := pkg.findVersion(version)
	if ver == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("version %s not found", version))
		return
	}
	if ver.DownloadURL == "" {
		ver.DownloadURL = h.downloadURL(name, version)
	}
	writeJSON(w, http.StatusOK, ver)
}

// handleDownload streams the zip (local store) or redirects to the CDN (R2).
func (h *handler) handleDownload(w http.ResponseWriter, r *http.Request, name, version string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if _, ok := h.optionalAuth(w, r); !ok {
		return
	}

	// For R2: the download URL is stored in metadata — redirect to CDN.
	if local, ok := isLocal(h.store.blobs); !ok {
		url := h.store.downloadURL(name, version)
		if url == "" {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
		redirectToBlob(w, r, url)
		return
	} else {
		// Local store: stream the file directly.
		path := local.localPath(blobKey(name, version))
		f, err := os.Open(path)
		if err != nil {
			writeError(w, http.StatusNotFound, "zip not found")
			return
		}
		defer f.Close()
		stat, _ := f.Stat()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s-%s.zip"`, name, version))
		if stat != nil {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
		}
		io.Copy(w, f) //nolint:errcheck
	}
}

// ─── POST /packages/:name/:version/publish ────────────────────────────────────

type publishRequest struct {
	Description      string            `json:"description"`
	Author           string            `json:"author"`
	License          string            `json:"license"`
	GeneroConstraint string            `json:"genero,omitempty"`
	FGLDeps          map[string]string `json:"fglDeps,omitempty"`
	JavaDeps         []javaDep         `json:"javaDeps,omitempty"`
	Checksum         string            `json:"checksum"`
}

type javaDep struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
	Checksum   string `json:"checksum,omitempty"`
}

func (h *handler) handlePublish(w http.ResponseWriter, r *http.Request, name, version string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	ar, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	if err := validateName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateVersion(version); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Ownership check — must happen before the duplicate check so the error
	// message is correct (forbidden beats conflict).
	if !h.auth.canPublish(name, ar.Username, ar.IsAdmin) {
		writeError(w, http.StatusForbidden,
			fmt.Sprintf("you are not an owner of %q", name))
		return
	}

	// Reject re-publishing an existing version.
	if pkg, err := h.store.loadPackage(name); err == nil {
		if pkg.findVersion(version) != nil {
			writeError(w, http.StatusConflict,
				fmt.Sprintf("version %s of %s already exists", version, name))
			return
		}
	}

	if err := r.ParseMultipartForm(128 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "cannot parse multipart form: "+err.Error())
		return
	}

	metaField := r.FormValue("meta")
	if metaField == "" {
		writeError(w, http.StatusBadRequest, `missing "meta" form field`)
		return
	}
	var meta publishRequest
	if err := json.Unmarshal([]byte(metaField), &meta); err != nil {
		writeError(w, http.StatusBadRequest, "invalid meta JSON: "+err.Error())
		return
	}

	zipFile, _, err := r.FormFile("zip")
	if err != nil {
		writeError(w, http.StatusBadRequest, `missing "zip" form file`)
		return
	}
	defer zipFile.Close()

	checksum, err := h.store.savePackage(name, version, meta, zipFile)
	if err != nil {
		log.Printf("publish error for %s@%s: %v", name, version, err)
		writeError(w, http.StatusInternalServerError, "failed to save package")
		return
	}

	// Verify client-declared checksum.
	if meta.Checksum != "" && !strings.EqualFold(checksum, meta.Checksum) {
		h.store.deleteVersion(name, version) //nolint:errcheck
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("checksum mismatch: declared %s, computed %s",
				meta.Checksum, checksum))
		return
	}

	// Claim ownership for new packages.
	h.auth.claimOwnership(name, ar.Username) //nolint:errcheck — no-op if already owned

	writeJSON(w, http.StatusCreated, map[string]string{
		"name":        name,
		"version":     version,
		"checksum":    checksum,
		"downloadUrl": h.downloadURL(name, version),
	})
	log.Printf("published %s@%s by %s (checksum: %s)", name, version, ar.Username, checksum)
}

// ─── /packages/:name/owners ───────────────────────────────────────────────────

func (h *handler) handleOwners(w http.ResponseWriter, r *http.Request, pkg string) {
	switch r.Method {
	case http.MethodGet:
		// Anyone (or any authenticated user if read auth is on) can list owners.
		if _, ok := h.optionalAuth(w, r); !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string][]string{
			"owners": h.auth.listOwners(pkg),
		})

	case http.MethodPost:
		ar, ok := h.requireAuth(w, r)
		if !ok {
			return
		}
		var req struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
			writeError(w, http.StatusBadRequest, "missing username")
			return
		}
		if err := h.auth.addOwner(pkg, req.Username, ar.Username, ar.IsAdmin); err != nil {
			status := http.StatusForbidden
			if strings.Contains(err.Error(), "does not exist") {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string][]string{
			"owners": h.auth.listOwners(pkg),
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

// DELETE /packages/:name/owners/:user
func (h *handler) handleOwnerRemove(w http.ResponseWriter, r *http.Request, pkg, target string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}
	ar, ok := h.requireAuth(w, r)
	if !ok {
		return
	}
	if err := h.auth.removeOwner(pkg, target, ar.Username, ar.IsAdmin); err != nil {
		status := http.StatusForbidden
		if strings.Contains(err.Error(), "last owner") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{
		"owners": h.auth.listOwners(pkg),
	})
}

// ─── GET /search ──────────────────────────────────────────────────────────────

func (h *handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if _, ok := h.optionalAuth(w, r); !ok {
		return
	}
	term := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if term == "" {
		writeError(w, http.StatusBadRequest, "missing query parameter q")
		return
	}
	writeJSON(w, http.StatusOK, h.store.search(term))
}

// ─── GET /health ──────────────────────────────────────────────────────────────

func (h *handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("package name cannot be empty")
	}
	for _, c := range name {
		if !isNameChar(c) {
			return fmt.Errorf("invalid character %q in package name", c)
		}
	}
	return nil
}

func validateVersion(version string) error {
	if version == "" {
		return fmt.Errorf("version cannot be empty")
	}
	if len(strings.Split(version, ".")) != 3 {
		return fmt.Errorf("version must be MAJOR.MINOR.PATCH")
	}
	return nil
}

func isNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d", r.Method, r.URL.Path, rw.status)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
