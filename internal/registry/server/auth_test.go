package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
        "net/http/httptest"
	"strings"
	"testing"

        "github.com/4js-mikefolcher/fglpkg/internal/registry/server"
)

// ─── Auth test helpers ────────────────────────────────────────────────────────

func authHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func doRequest(t *testing.T, method, url, token, body string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("JSON decode: %v", err)
	}
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want %d\nbody: %s", resp.StatusCode, want, body)
	}
}

// ─── /auth/token (create) ─────────────────────────────────────────────────────

func TestCreateUserAdminOnly(t *testing.T) {
	ts := newTestServer(t)

	// Admin can create a user.
	body := `{"username":"alice","email":"alice@example.com"}`
	resp := doRequest(t, http.MethodPost, ts.URL+"/auth/token", testToken, body)
	assertStatus(t, resp, http.StatusCreated)

	var result map[string]string
	decodeJSON(t, resp, &result)
	if result["token"] == "" {
		t.Error("expected token in response, got empty")
	}
	if result["username"] != "alice" {
		t.Errorf("username = %q, want %q", result["username"], "alice")
	}
}

func TestCreateUserNonAdminForbidden(t *testing.T) {
	ts := newTestServer(t)

	// Create alice via admin.
	aliceToken := createUser(t, ts, "alice", testToken)

	// Alice tries to create bob directly — forbidden.
	body := `{"username":"bob"}`
	resp := doRequest(t, http.MethodPost, ts.URL+"/auth/token", aliceToken, body)
	assertStatus(t, resp, http.StatusForbidden)
}

func TestCreateUserDuplicateRejected(t *testing.T) {
	ts := newTestServer(t)
	createUser(t, ts, "alice", testToken)

	body := `{"username":"alice"}`
	resp := doRequest(t, http.MethodPost, ts.URL+"/auth/token", testToken, body)
	assertStatus(t, resp, http.StatusConflict)
}

func TestCreateUserInvalidUsername(t *testing.T) {
	ts := newTestServer(t)
	body := `{"username":"Alice Jones"}` // spaces not allowed
	resp := doRequest(t, http.MethodPost, ts.URL+"/auth/token", testToken, body)
	assertStatus(t, resp, http.StatusBadRequest)
}

func TestCreateUserUnauthenticated(t *testing.T) {
	ts := newTestServer(t)
	body := `{"username":"alice"}`
	resp := doRequest(t, http.MethodPost, ts.URL+"/auth/token", "", body)
	assertStatus(t, resp, http.StatusUnauthorized)
}

// ─── /auth/whoami ─────────────────────────────────────────────────────────────

func TestWhoamiAdmin(t *testing.T) {
	ts := newTestServer(t)
	resp := doRequest(t, http.MethodGet, ts.URL+"/auth/whoami", testToken, "")
	assertStatus(t, resp, http.StatusOK)

	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["username"] != "admin" {
		t.Errorf("username = %q, want %q", result["username"], "admin")
	}
	if result["isAdmin"] != true {
		t.Errorf("isAdmin = %v, want true", result["isAdmin"])
	}
}

func TestWhoamiUser(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)

	resp := doRequest(t, http.MethodGet, ts.URL+"/auth/whoami", aliceToken, "")
	assertStatus(t, resp, http.StatusOK)

	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["username"] != "alice" {
		t.Errorf("username = %q, want %q", result["username"], "alice")
	}
	if result["isAdmin"] != false {
		t.Errorf("isAdmin = %v, want false", result["isAdmin"])
	}
}

func TestWhoamiUnauthenticated(t *testing.T) {
	ts := newTestServer(t)
	resp := doRequest(t, http.MethodGet, ts.URL+"/auth/whoami", "", "")
	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestWhoamiInvalidToken(t *testing.T) {
	ts := newTestServer(t)
	resp := doRequest(t, http.MethodGet, ts.URL+"/auth/whoami", "bad-token", "")
	assertStatus(t, resp, http.StatusUnauthorized)
}

// ─── /auth/token DELETE (revoke) ──────────────────────────────────────────────

func TestRevokeOwnToken(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)

	// Alice revokes her own token.
	resp := doRequest(t, http.MethodDelete, ts.URL+"/auth/token", aliceToken, "")
	assertStatus(t, resp, http.StatusOK)

	// Token should now be invalid.
	resp2 := doRequest(t, http.MethodGet, ts.URL+"/auth/whoami", aliceToken, "")
	assertStatus(t, resp2, http.StatusUnauthorized)
}

func TestAdminRevokesUserToken(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)

	body := `{"username":"alice"}`
	resp := doRequest(t, http.MethodDelete, ts.URL+"/auth/token", testToken, body)
	assertStatus(t, resp, http.StatusOK)

	resp2 := doRequest(t, http.MethodGet, ts.URL+"/auth/whoami", aliceToken, "")
	assertStatus(t, resp2, http.StatusUnauthorized)
}

func TestNonAdminCannotRevokeOtherToken(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)
	createUser(t, ts, "bob", testToken)

	// Alice tries to revoke bob.
	body := `{"username":"bob"}`
	resp := doRequest(t, http.MethodDelete, ts.URL+"/auth/token", aliceToken, body)
	assertStatus(t, resp, http.StatusForbidden)
}

// ─── /auth/token/rotate ───────────────────────────────────────────────────────

func TestRotateToken(t *testing.T) {
	ts := newTestServer(t)
	oldToken := createUser(t, ts, "alice", testToken)

	resp := doRequest(t, http.MethodPost, ts.URL+"/auth/token/rotate", oldToken, "")
	assertStatus(t, resp, http.StatusOK)

	var result map[string]string
	decodeJSON(t, resp, &result)
	newToken := result["token"]
	if newToken == "" {
		t.Fatal("expected new token, got empty")
	}
	if newToken == oldToken {
		t.Error("rotated token should differ from old token")
	}

	// Old token should be invalid.
	resp2 := doRequest(t, http.MethodGet, ts.URL+"/auth/whoami", oldToken, "")
	assertStatus(t, resp2, http.StatusUnauthorized)

	// New token should work.
	resp3 := doRequest(t, http.MethodGet, ts.URL+"/auth/whoami", newToken, "")
	assertStatus(t, resp3, http.StatusOK)
}

// ─── /auth/users (admin list) ─────────────────────────────────────────────────

func TestListUsersAdminOnly(t *testing.T) {
	ts := newTestServer(t)
	createUser(t, ts, "alice", testToken)
	createUser(t, ts, "bob", testToken)

	resp := doRequest(t, http.MethodGet, ts.URL+"/auth/users", testToken, "")
	assertStatus(t, resp, http.StatusOK)

	var result map[string][]string
	decodeJSON(t, resp, &result)
	if len(result["users"]) != 2 {
		t.Errorf("expected 2 users, got %d", len(result["users"]))
	}
}

func TestListUsersNonAdminForbidden(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)
	resp := doRequest(t, http.MethodGet, ts.URL+"/auth/users", aliceToken, "")
	assertStatus(t, resp, http.StatusForbidden)
}

// ─── Ownership: /packages/:name/owners ───────────────────────────────────────

func TestFirstPublisherBecomesOwner(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)

	// Alice publishes myutils — she should become the owner.
	resp := publish(t, ts, "myutils", "1.0.0", map[string]any{}, aliceToken)
	assertStatus(t, resp, http.StatusCreated)

	// Check ownership.
	owners := listOwners(t, ts, "myutils", aliceToken)
	if len(owners) != 1 || owners[0] != "alice" {
		t.Errorf("owners = %v, want [alice]", owners)
	}
}

func TestNonOwnerCannotPublish(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)
	bobToken := createUser(t, ts, "bob", testToken)

	// Alice publishes v1, claims ownership.
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, aliceToken)

	// Bob tries to publish v2 — should be forbidden.
	resp := publish(t, ts, "myutils", "2.0.0", map[string]any{}, bobToken)
	assertStatus(t, resp, http.StatusForbidden)
}

func TestAdminCanAlwaysPublish(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)

	// Alice owns the package.
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, aliceToken)

	// Admin can still publish.
	resp := publish(t, ts, "myutils", "2.0.0", map[string]any{}, testToken)
	assertStatus(t, resp, http.StatusCreated)
}

func TestOwnerCanAddAnotherOwner(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)
	createUser(t, ts, "bob", testToken)

	publish(t, ts, "myutils", "1.0.0", map[string]any{}, aliceToken)

	// Alice adds bob as owner.
	body := `{"username":"bob"}`
	resp := doRequest(t, http.MethodPost, ts.URL+"/packages/myutils/owners", aliceToken, body)
	assertStatus(t, resp, http.StatusOK)

	owners := listOwners(t, ts, "myutils", aliceToken)
	ownerSet := make(map[string]bool)
	for _, o := range owners {
		ownerSet[o] = true
	}
	if !ownerSet["alice"] || !ownerSet["bob"] {
		t.Errorf("owners = %v, want [alice bob]", owners)
	}
}

func TestNonOwnerCannotAddOwner(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)
	bobToken := createUser(t, ts, "bob", testToken)
	createUser(t, ts, "carol", testToken)

	publish(t, ts, "myutils", "1.0.0", map[string]any{}, aliceToken)

	// Bob (not an owner) tries to add carol.
	body := `{"username":"carol"}`
	resp := doRequest(t, http.MethodPost, ts.URL+"/packages/myutils/owners", bobToken, body)
	assertStatus(t, resp, http.StatusForbidden)
}

func TestCannotRemoveLastOwner(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, aliceToken)

	// Alice tries to remove herself (the only owner).
	resp := doRequest(t, http.MethodDelete,
		ts.URL+"/packages/myutils/owners/alice", aliceToken, "")
	assertStatus(t, resp, http.StatusConflict)
}

func TestRemoveOwner(t *testing.T) {
	ts := newTestServer(t)
	aliceToken := createUser(t, ts, "alice", testToken)
	createUser(t, ts, "bob", testToken)

	publish(t, ts, "myutils", "1.0.0", map[string]any{}, aliceToken)

	// Add bob.
	doRequest(t, http.MethodPost, ts.URL+"/packages/myutils/owners", aliceToken, `{"username":"bob"}`)

	// Remove bob.
	resp := doRequest(t, http.MethodDelete,
		ts.URL+"/packages/myutils/owners/bob", aliceToken, "")
	assertStatus(t, resp, http.StatusOK)

	owners := listOwners(t, ts, "myutils", aliceToken)
	for _, o := range owners {
		if o == "bob" {
			t.Error("bob should have been removed from owners")
		}
	}
}

// ─── Invite flow ─────────────────────────────────────────────────────────────

// TestInviteFlow validates the full admin-bootstrap → owner-invite chain:
// admin creates alice, alice publishes a package, alice invites bob,
// bob can publish to the same package.
func TestInviteFlow(t *testing.T) {
	ts := newTestServer(t)

	// Admin creates alice.
	aliceToken := createUser(t, ts, "alice", testToken)

	// Alice publishes and becomes owner.
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, aliceToken)

	// Alice invites bob — but bob must first be created by admin.
	bobToken := createUser(t, ts, "bob", testToken)

	// Alice adds bob as owner.
	doRequest(t, http.MethodPost, ts.URL+"/packages/myutils/owners",
		aliceToken, `{"username":"bob"}`)

	// Bob can now publish.
	resp := publish(t, ts, "myutils", "2.0.0", map[string]any{}, bobToken)
	assertStatus(t, resp, http.StatusCreated)
}

// ─── Read auth ────────────────────────────────────────────────────────────────

func TestReadAuthDisabledAllowsAnonymous(t *testing.T) {
	// Default test server has RequireReadAuth=false.
	ts := newTestServer(t)
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, testToken)

	// Anonymous read should succeed.
	resp, _ := http.Get(ts.URL + "/packages/myutils/versions")
	assertStatus(t, resp, http.StatusOK)
}

func TestReadAuthEnabledBlocksAnonymous(t *testing.T) {
	ts := newTestServerWithReadAuth(t)
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, testToken)

	// Anonymous read should fail.
	resp, _ := http.Get(ts.URL + "/packages/myutils/versions")
	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestReadAuthEnabledAllowsValidToken(t *testing.T) {
	ts := newTestServerWithReadAuth(t)
	aliceToken := createUser(t, ts, "alice", testToken)
	publish(t, ts, "myutils", "1.0.0", map[string]any{}, testToken)

	resp := doRequest(t, http.MethodGet, ts.URL+"/packages/myutils/versions", aliceToken, "")
	assertStatus(t, resp, http.StatusOK)
}

// ─── Test helpers ─────────────────────────────────────────────────────────────

// createUser calls POST /auth/token as admin and returns the new user's token.
func createUser(t *testing.T, ts *httptest.Server, username, adminToken string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q}`, username)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/auth/token",
		strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("createUser %s: %v", username, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("createUser %s: status %d: %s", username, resp.StatusCode, body)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	return result["token"]
}

// listOwners calls GET /packages/:name/owners and returns the owner list.
func listOwners(t *testing.T, ts *httptest.Server, pkg, token string) []string {
	t.Helper()
	resp := doRequest(t, http.MethodGet, ts.URL+"/packages/"+pkg+"/owners", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listOwners %s: status %d", pkg, resp.StatusCode)
	}
	var result map[string][]string
	decodeJSON(t, resp, &result)
	return result["owners"]
}

func newTestServerWithReadAuth(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := server.Config{
		DataDir:         t.TempDir(),
		PublishToken:    testToken,
		RequireReadAuth: true,
	}
	srv, err := server.NewTestServer(cfg)
	if err != nil {
		t.Fatalf("newTestServerWithReadAuth: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}
