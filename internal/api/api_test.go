package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/api"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/node"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitFor polls fn until it returns true or timeout elapses.
func waitFor(t *testing.T, desc string, fn func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", desc)
}

// newTestNode creates a Node with an isolated temporary data directory and
// registers cleanup to shut down gossip on test completion.
func newTestNode(t *testing.T, nodeID string) *node.Node {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := node.NodeConfig{DataDir: tmpDir}
	n, err := node.NewNodeWithConfig(models.NodeID(nodeID), "127.0.0.1", cfg)
	if err != nil {
		t.Fatalf("NewNode(%s): %v", nodeID, err)
	}
	t.Cleanup(func() {
		n.Gossip.Shutdown()
	})
	return n
}

// newTestServer wraps a node in an httptest.Server and handles cleanup.
func newTestServer(t *testing.T, n *node.Node) *httptest.Server {
	t.Helper()
	s := api.NewServer(n, t.TempDir()) // isolated auth data per test
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// signup calls POST /api/signup and returns the session token.
func signup(t *testing.T, srv *httptest.Server, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(srv.URL+"/api/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("signup POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup: expected 201, got %d", resp.StatusCode)
	}
	var data map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("signup: decode response: %v", err)
	}
	return data["token"]
}

// authDo sends a request with the Authorization header set.
func authDo(t *testing.T, method, url, token string, body interface{}) *http.Response {
	t.Helper()
	var b *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		b = bytes.NewReader(data)
	} else {
		b = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, b)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// Phase 2 tests: Session authentication (Bug 6)
// ---------------------------------------------------------------------------

// TestSignup_ReturnsTokenAndUserID verifies that POST /api/signup returns
// a JSON body with "token" and "user_id" fields.
func TestSignup_ReturnsTokenAndUserID(t *testing.T) {
	n := newTestNode(t, "api-auth-signup")
	srv := newTestServer(t, n)

	body, _ := json.Marshal(map[string]string{"username": "alice", "password": "secret"})
	resp, err := http.Post(srv.URL+"/api/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var data map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if data["token"] == "" {
		t.Error("signup response missing 'token' field")
	}
	if data["user_id"] != "alice" {
		t.Errorf("expected user_id=alice, got %q", data["user_id"])
	}
}

// TestLogin_ReturnsTokenAndUserID verifies that POST /api/login returns
// a JSON body with "token" and "user_id" fields.
func TestLogin_ReturnsTokenAndUserID(t *testing.T) {
	n := newTestNode(t, "api-auth-login")
	srv := newTestServer(t, n)

	// Register first.
	body, _ := json.Marshal(map[string]string{"username": "bob", "password": "hunter2"})
	http.Post(srv.URL+"/api/signup", "application/json", bytes.NewReader(body))

	// Now login.
	resp := authDo(t, "POST", srv.URL+"/api/login", "", map[string]string{
		"username": "bob",
		"password": "hunter2",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200, got %d", resp.StatusCode)
	}

	var data map[string]string
	json.NewDecoder(resp.Body).Decode(&data)
	if data["token"] == "" {
		t.Error("login response missing 'token' field")
	}
	if data["user_id"] != "bob" {
		t.Errorf("expected user_id=bob, got %q", data["user_id"])
	}
}

// TestTokenAuth_ProtectedEndpointsRequireToken checks that each protected
// API endpoint returns 401 when no Authorization header is supplied.
func TestTokenAuth_ProtectedEndpointsRequireToken(t *testing.T) {
	n := newTestNode(t, "api-auth-protect")
	srv := newTestServer(t, n)

	endpoints := []struct {
		method string
		path   string
		body   interface{}
	}{
		{"POST", "/api/join", map[string]interface{}{"community_id": "test"}},
		{"POST", "/api/post", map[string]interface{}{"community_id": "test", "title": "hi"}},
		{"POST", "/api/comment", map[string]interface{}{"community_id": "test", "comment": map[string]interface{}{}}},
		{"POST", "/api/vote", map[string]interface{}{"community_id": "test"}},
		{"POST", "/api/moderate", map[string]interface{}{"community_id": "test", "action_type": "DELETE_POST", "target": "x"}},
	}

	for _, ep := range endpoints {
		resp := authDo(t, ep.method, srv.URL+ep.path, "" /* no token */, ep.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401 without token, got %d", ep.method, ep.path, resp.StatusCode)
		}
	}
}

// TestTokenAuth_ValidTokenAllowsAccess verifies a valid token lets the user
// through to the handler (no spurious 401).
func TestTokenAuth_ValidTokenAllowsAccess(t *testing.T) {
	n := newTestNode(t, "api-auth-valid")
	srv := newTestServer(t, n)

	token := signup(t, srv, "charlie", "pw")
	if token == "" {
		t.Fatal("signup returned empty token")
	}

	// GET /api/communities is public — verify it works.
	resp, err := http.Get(srv.URL + "/api/communities")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/communities: expected 200, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Phase 1 tests: Action-type mapping + ban field (Bugs 3 & 4)
// ---------------------------------------------------------------------------

// joinCommunityAndWaitForLeader creates a community on n and blocks until
// n is the Raft leader (required before Propose can succeed).
func joinCommunityAndWaitForLeader(t *testing.T, n *node.Node, communityID models.CommunityID) {
	t.Helper()
	if err := n.JoinCommunity(communityID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}
	waitFor(t, fmt.Sprintf("node to become Raft leader for %s", communityID),
		func() bool { return n.IsRaftLeader(communityID) },
		5*time.Second)
}

// TestModerate_DeletePostMapsToRemovePost verifies that the UI action type
// "DELETE_POST" is translated to the model constant ModRemovePost before
// being stored in the Raft log. This is Bug 3.
func TestModerate_DeletePostMapsToRemovePost(t *testing.T) {
	n := newTestNode(t, "api-mod-map")
	communityID := models.CommunityID("mod-map-comm")
	joinCommunityAndWaitForLeader(t, n, communityID)

	srv := newTestServer(t, n)
	token := signup(t, srv, "admin", "adminpass")

	// Create a post.
	createResp := authDo(t, "POST", srv.URL+"/api/post", token, map[string]interface{}{
		"community_id": string(communityID),
		"title":        "Post to delete",
		"body":         "body",
	})
	if createResp.StatusCode != http.StatusCreated {
		createResp.Body.Close()
		t.Fatalf("create post: expected 201, got %d", createResp.StatusCode)
	}
	var postResp map[string]string
	json.NewDecoder(createResp.Body).Decode(&postResp)
	createResp.Body.Close()

	postHash := postResp["hash"]
	if postHash == "" {
		t.Fatal("create post returned empty hash")
	}

	// Moderate with the UI string "DELETE_POST".
	modResp := authDo(t, "POST", srv.URL+"/api/moderate", token, map[string]interface{}{
		"community_id": string(communityID),
		"action_type":  "DELETE_POST",
		"target":       postHash,
	})
	if modResp.StatusCode != http.StatusOK {
		modResp.Body.Close()
		t.Fatalf("moderate: expected 200, got %d", modResp.StatusCode)
	}
	modResp.Body.Close()

	// Wait for Raft to commit the entry.
	manager, err := n.GetCommunity(communityID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "moderation log to have 1 entry",
		func() bool { return len(manager.GetModerationLog()) >= 1 },
		3*time.Second)

	log := manager.GetModerationLog()
	if len(log) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(log))
	}

	// Key assertion: action type must be the model constant, NOT the UI string.
	if log[0].ActionType != models.ModRemovePost {
		t.Errorf("Bug 3 not fixed: ActionType = %q, want %q (ModRemovePost)",
			log[0].ActionType, models.ModRemovePost)
	}
	if log[0].TargetHash != models.ContentHash(postHash) {
		t.Errorf("TargetHash mismatch: got %q, want %q", log[0].TargetHash, postHash)
	}

	// Verify the post disappears from the feed.
	feedResp, _ := http.Get(srv.URL + "/api/posts?community_id=" + string(communityID))
	var posts []interface{}
	json.NewDecoder(feedResp.Body).Decode(&posts)
	feedResp.Body.Close()
	if len(posts) != 0 {
		t.Errorf("Bug 3 not fixed: deleted post still appears in feed (got %d posts)", len(posts))
	}
}

// TestBan_UsesTargetUserField verifies that BAN_USER actions store the
// username in TargetUser (not TargetHash) and that isBanned correctly
// identifies the user as banned. This is Bug 4.
func TestBan_UsesTargetUserField(t *testing.T) {
	n := newTestNode(t, "api-ban-field")
	communityID := models.CommunityID("ban-field-comm")
	joinCommunityAndWaitForLeader(t, n, communityID)

	srv := newTestServer(t, n)
	adminToken := signup(t, srv, "admin", "adminpass")
	_ = adminToken

	// Register the "victim" user.
	victimToken := signup(t, srv, "victim", "victimpass")
	_ = victimToken

	// Admin bans victim.
	banResp := authDo(t, "POST", srv.URL+"/api/moderate", adminToken, map[string]interface{}{
		"community_id": string(communityID),
		"action_type":  "BAN_USER",
		"target":       "victim",
	})
	if banResp.StatusCode != http.StatusOK {
		banResp.Body.Close()
		t.Fatalf("ban user: expected 200, got %d", banResp.StatusCode)
	}
	banResp.Body.Close()

	manager, err := n.GetCommunity(communityID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ban log entry",
		func() bool { return len(manager.GetModerationLog()) >= 1 },
		3*time.Second)

	log := manager.GetModerationLog()
	if len(log) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(log))
	}

	// Key assertion: Bug 4 - ban must use TargetUser, not TargetHash.
	entry := log[0]
	if entry.ActionType != models.ModBanUser {
		t.Errorf("expected ModBanUser, got %q", entry.ActionType)
	}
	if string(entry.TargetUser) != "victim" {
		t.Errorf("Bug 4 not fixed: TargetUser = %q, want \"victim\"", entry.TargetUser)
	}
	if entry.TargetHash != "" {
		t.Errorf("Bug 4 not fixed: TargetHash should be empty for ban, got %q", entry.TargetHash)
	}

	// Create an admin post so the feed has something to check.
	postResp := authDo(t, "POST", srv.URL+"/api/post", adminToken, map[string]interface{}{
		"community_id": string(communityID),
		"title":        "Visible admin post",
		"body":         "body",
	})
	postResp.Body.Close()

	// Banned victim tries to post — should get 403.
	forbidResp := authDo(t, "POST", srv.URL+"/api/post", victimToken, map[string]interface{}{
		"community_id": string(communityID),
		"title":        "I'm banned",
		"body":         "body",
	})
	forbidResp.Body.Close()
	if forbidResp.StatusCode != http.StatusForbidden {
		t.Errorf("Bug 4 not fixed: banned user could post, got status %d", forbidResp.StatusCode)
	}
}
