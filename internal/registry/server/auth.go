// Package auth implements token-based authentication and package ownership
// for the fglpkg registry server.
//
// Design:
//   - Tokens are cryptographically random 32-byte hex strings (64 chars).
//   - Only SHA256(token) is stored on disk — raw tokens are never persisted.
//   - The admin bootstrap token is set via server config / env var and also
//     stored only as a hash, so the running process never holds it in memory
//     beyond the config loading phase.
//   - Package ownership: first publisher of a package becomes its sole owner.
//     Owners can add/remove other owners. The admin token bypasses ownership.
//   - Read authentication is optional and controlled by Config.RequireReadAuth.
package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── Token generation ─────────────────────────────────────────────────────────

// generateToken returns a cryptographically random 64-character hex token.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// hashToken returns the lowercase hex SHA256 of a raw token string.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// ─── Data model ───────────────────────────────────────────────────────────────

// userRecord represents a single user stored in auth/users.json.
type userRecord struct {
	Username  string `json:"username"`
	Email     string `json:"email,omitempty"`
	TokenHash string `json:"tokenHash"` // SHA256(rawToken)
	CreatedAt string `json:"createdAt"`
	CreatedBy string `json:"createdBy"` // username of the inviting user, or "admin"
}

// usersFile is the structure of auth/users.json.
type usersFile struct {
	Users []*userRecord `json:"users"`
}

func (f *usersFile) findByHash(hash string) *userRecord {
	for _, u := range f.Users {
		if u.TokenHash == hash {
			return u
		}
	}
	return nil
}

func (f *usersFile) findByUsername(name string) *userRecord {
	for _, u := range f.Users {
		if u.Username == name {
			return u
		}
	}
	return nil
}

// ownershipFile is the structure of auth/ownership.json.
// Maps package name → list of owner usernames.
type ownershipFile struct {
	Packages map[string][]string `json:"packages"`
}

func (f *ownershipFile) owners(pkg string) []string {
	if f.Packages == nil {
		return nil
	}
	return f.Packages[pkg]
}

func (f *ownershipFile) isOwner(pkg, username string) bool {
	for _, o := range f.owners(pkg) {
		if o == username {
			return true
		}
	}
	return false
}

func (f *ownershipFile) addOwner(pkg, username string) {
	if f.Packages == nil {
		f.Packages = make(map[string][]string)
	}
	if !f.isOwner(pkg, username) {
		f.Packages[pkg] = append(f.Packages[pkg], username)
	}
}

func (f *ownershipFile) removeOwner(pkg, username string) {
	owners := f.owners(pkg)
	filtered := owners[:0]
	for _, o := range owners {
		if o != username {
			filtered = append(filtered, o)
		}
	}
	f.Packages[pkg] = filtered
}

// ─── authStore ────────────────────────────────────────────────────────────────

// authStore manages users and ownership, backed by two JSON files.
type authStore struct {
	authDir       string
	adminHash     string // SHA256 of the admin bootstrap token
	mu            sync.RWMutex
	users         usersFile
	ownership     ownershipFile
}

func newAuthStore(dataDir, adminToken string) (*authStore, error) {
	dir := filepath.Join(dataDir, "auth")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create auth dir: %w", err)
	}

	s := &authStore{
		authDir:   dir,
		adminHash: hashToken(adminToken),
	}

	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// loadLocked reads both backing files from disk. Caller must hold no lock
// (called only during init and after writes).
func (s *authStore) loadLocked() error {
	// users.json
	if data, err := os.ReadFile(s.usersPath()); err == nil {
		if err := json.Unmarshal(data, &s.users); err != nil {
			return fmt.Errorf("invalid users.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot read users.json: %w", err)
	}

	// ownership.json
	if data, err := os.ReadFile(s.ownershipPath()); err == nil {
		if err := json.Unmarshal(data, &s.ownership); err != nil {
			return fmt.Errorf("invalid ownership.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot read ownership.json: %w", err)
	}

	if s.ownership.Packages == nil {
		s.ownership.Packages = make(map[string][]string)
	}
	return nil
}

// ─── Authentication ───────────────────────────────────────────────────────────

// authResult is returned from authenticate.
type authResult struct {
	Username string
	IsAdmin  bool
}

// authenticate resolves a raw token to an authResult.
// Returns an error if the token is invalid or empty.
func (s *authStore) authenticate(rawToken string) (authResult, error) {
	if rawToken == "" {
		return authResult{}, fmt.Errorf("no token provided")
	}
	h := hashToken(rawToken)

	// Admin bootstrap token check (constant-time via hash comparison).
	if h == s.adminHash {
		return authResult{Username: "admin", IsAdmin: true}, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	u := s.users.findByHash(h)
	if u == nil {
		return authResult{}, fmt.Errorf("invalid token")
	}
	return authResult{Username: u.Username, IsAdmin: false}, nil
}

// tokenFromRequest extracts the raw Bearer token from an HTTP request.
func tokenFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// ─── User management ──────────────────────────────────────────────────────────

// createUser adds a new user and returns their raw token.
// Only the admin or an existing user (inviter) may call this.
func (s *authStore) createUser(username, email, createdBy string) (string, error) {
	if err := validateUsername(username); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.users.findByUsername(username) != nil {
		return "", fmt.Errorf("username %q already exists", username)
	}

	rawToken, err := generateToken()
	if err != nil {
		return "", err
	}

	s.users.Users = append(s.users.Users, &userRecord{
		Username:  username,
		Email:     email,
		TokenHash: hashToken(rawToken),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		CreatedBy: createdBy,
	})

	if err := atomicWriteJSON(s.usersPath(), &s.users); err != nil {
		// Roll back the in-memory change.
		s.users.Users = s.users.Users[:len(s.users.Users)-1]
		return "", fmt.Errorf("cannot persist user: %w", err)
	}
	return rawToken, nil
}

// revokeToken invalidates the token belonging to username.
// Admin may revoke anyone's token; a user may only revoke their own.
func (s *authStore) revokeToken(callerUsername, targetUsername string, callerIsAdmin bool) error {
	if !callerIsAdmin && callerUsername != targetUsername {
		return fmt.Errorf("only admin can revoke another user's token")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u := s.users.findByUsername(targetUsername)
	if u == nil {
		return fmt.Errorf("user %q not found", targetUsername)
	}
	u.TokenHash = "" // empty hash matches nothing
	return atomicWriteJSON(s.usersPath(), &s.users)
}

// rotateToken generates a new token for username and returns it.
func (s *authStore) rotateToken(callerUsername, targetUsername string, callerIsAdmin bool) (string, error) {
	if !callerIsAdmin && callerUsername != targetUsername {
		return "", fmt.Errorf("only admin can rotate another user's token")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u := s.users.findByUsername(targetUsername)
	if u == nil {
		return "", fmt.Errorf("user %q not found", targetUsername)
	}

	rawToken, err := generateToken()
	if err != nil {
		return "", err
	}
	u.TokenHash = hashToken(rawToken)
	if err := atomicWriteJSON(s.usersPath(), &s.users); err != nil {
		return "", fmt.Errorf("cannot persist rotated token: %w", err)
	}
	return rawToken, nil
}

// listUsers returns all usernames (admin only).
func (s *authStore) listUsers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, len(s.users.Users))
	for i, u := range s.users.Users {
		names[i] = u.Username
	}
	return names
}

// ─── Ownership ────────────────────────────────────────────────────────────────

// claimOwnership sets username as the first owner of pkg.
// Returns an error if the package already has owners (already claimed).
func (s *authStore) claimOwnership(pkg, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.ownership.owners(pkg)) > 0 {
		return fmt.Errorf("package %q already has owners", pkg)
	}
	s.ownership.addOwner(pkg, username)
	return atomicWriteJSON(s.ownershipPath(), &s.ownership)
}

// canPublish reports whether username is allowed to publish to pkg.
// Admin always can. For a new package (no owners yet), anyone authenticated can.
func (s *authStore) canPublish(pkg, username string, isAdmin bool) bool {
	if isAdmin {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	owners := s.ownership.owners(pkg)
	if len(owners) == 0 {
		return true // first publisher claims it
	}
	return s.ownership.isOwner(pkg, username)
}

// addOwner adds newOwner to pkg's owner list.
// Caller must already be an owner or admin.
func (s *authStore) addOwner(pkg, newOwner, callerUsername string, callerIsAdmin bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !callerIsAdmin && !s.ownership.isOwner(pkg, callerUsername) {
		return fmt.Errorf("you are not an owner of %q", pkg)
	}
	if s.users.findByUsername(newOwner) == nil {
		return fmt.Errorf("user %q does not exist", newOwner)
	}
	s.ownership.addOwner(pkg, newOwner)
	return atomicWriteJSON(s.ownershipPath(), &s.ownership)
}

// removeOwner removes targetOwner from pkg's owner list.
// Caller must be an owner or admin. Cannot remove the last owner.
func (s *authStore) removeOwner(pkg, targetOwner, callerUsername string, callerIsAdmin bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !callerIsAdmin && !s.ownership.isOwner(pkg, callerUsername) {
		return fmt.Errorf("you are not an owner of %q", pkg)
	}
	owners := s.ownership.owners(pkg)
	if len(owners) <= 1 {
		return fmt.Errorf("cannot remove the last owner of %q", pkg)
	}
	s.ownership.removeOwner(pkg, targetOwner)
	return atomicWriteJSON(s.ownershipPath(), &s.ownership)
}

// listOwners returns the owner list for a package.
func (s *authStore) listOwners(pkg string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	owners := s.ownership.owners(pkg)
	out := make([]string, len(owners))
	copy(out, owners)
	return out
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *authStore) usersPath() string {
	return filepath.Join(s.authDir, "users.json")
}

func (s *authStore) ownershipPath() string {
	return filepath.Join(s.authDir, "ownership.json")
}

func validateUsername(name string) error {
	if name == "" {
		return fmt.Errorf("username cannot be empty")
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return fmt.Errorf("invalid character %q in username (use a-z, 0-9, - _)", c)
		}
	}
	return nil
}
