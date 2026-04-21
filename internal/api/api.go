package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/node"
)

type Server struct {
	node     *node.Node
	mu       sync.RWMutex
	users    map[string]string // username -> hashed password
	tokens   map[string]string // session token -> username
	authFile string
}

type authState struct {
	Users  map[string]string `json:"users"`
	Tokens map[string]string `json:"tokens"`
}

func NewServer(n *node.Node, dataDir string) *Server {
	authFile := "users.json"
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0755); err == nil {
			authFile = filepath.Join(dataDir, "users.json")
		}
	}
	s := &Server{
		node:     n,
		users:    make(map[string]string),
		tokens:   make(map[string]string),
		authFile: authFile,
	}
	s.loadUsers()
	return s
}

// --- User Account Management ---

func (s *Server) loadUsers() {
	data, err := os.ReadFile(s.authFile)
	if err != nil {
		return
	}

	var state authState
	if err := json.Unmarshal(data, &state); err == nil && (state.Users != nil || state.Tokens != nil) {
		if state.Users != nil {
			s.users = state.Users
		}
		if state.Tokens != nil {
			s.tokens = state.Tokens
		}
		return
	}

	var legacyUsers map[string]string
	if err := json.Unmarshal(data, &legacyUsers); err == nil && legacyUsers != nil {
		s.users = legacyUsers
	}
}

func (s *Server) saveUsers() {
	data, _ := json.MarshalIndent(authState{Users: s.users, Tokens: s.tokens}, "", "  ")
	_ = os.WriteFile(s.authFile, data, 0644)
}

func hashPassword(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
}

// generateToken creates a cryptographically secure 32-byte hex token.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// validateToken reads the Authorization: Bearer <token> header and returns the
// authenticated username. Returns ("", false) if the token is missing or invalid.
func (s *Server) validateToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if len(auth) < 8 || auth[:7] != "Bearer " {
		return "", false
	}
	token := auth[7:]
	s.mu.RLock()
	defer s.mu.RUnlock()
	username, ok := s.tokens[token]
	return username, ok
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[req.Username]; exists {
		http.Error(w, "Username is already taken", http.StatusConflict)
		return
	}

	token, err := generateToken()
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	s.users[req.Username] = hashPassword(req.Password)
	s.tokens[token] = req.Username
	s.saveUsers()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"token": token, "user_id": req.Username})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	expectedHash, exists := s.users[req.Username]
	if !exists || expectedHash != hashPassword(req.Password) {
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	token, err := generateToken()
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	s.tokens[token] = req.Username
	s.saveUsers()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"token": token, "user_id": req.Username})
}

// --- Moderation Helpers ---

func isBanned(logs []models.ModerationAction, userID string) bool {
	banned := false
	for _, entry := range logs {
		if string(entry.TargetUser) == userID {
			if entry.ActionType == models.ModBanUser {
				banned = true
			} else if entry.ActionType == models.ModUnbanUser {
				banned = false
			}
		}
	}
	return banned
}

// --- Main Server Setup ---

// Handler builds and returns the HTTP request handler.
// Exported so tests can use httptest.NewServer(server.Handler()).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("./ui")))
	mux.HandleFunc("/api/signup", s.handleSignup)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/communities", s.handleGetCommunities)
	mux.HandleFunc("/api/join", s.handleJoinCommunity)
	mux.HandleFunc("/api/posts", s.handleGetPosts)
	mux.HandleFunc("/api/post", s.handleCreatePost)
	mux.HandleFunc("/api/comments", s.handleGetComments)
	mux.HandleFunc("/api/comment", s.handleCreateComment)
	mux.HandleFunc("/api/vote", s.handleVote)
	mux.HandleFunc("/api/moderate", s.handleModerate)
	mux.HandleFunc("/api/status", s.handleStatus)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) Start(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}

// handleStatus returns a JSON snapshot of this node's identity and cluster view.
// It is unauthenticated so the test script can poll it before sending any requests.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	local := s.node.Gossip.LocalNode()
	gossipAddr := local.Address()

	members := s.node.Gossip.Members()
	memberAddrs := make([]string, 0, len(members))
	for _, m := range members {
		memberAddrs = append(memberAddrs, m.Address())
	}

	communities := s.node.GetJoinedCommunities()
	commStrs := make([]string, len(communities))
	for i, c := range communities {
		commStrs[i] = string(c)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"node_id":      string(s.node.NodeID),
		"gossip_addr":  gossipAddr,
		"gossip_port":  local.Port,
		"gossip_peers": memberAddrs,
		"member_count": len(members),
		"communities":  commStrs,
	})
}

func (s *Server) handleGetCommunities(w http.ResponseWriter, r *http.Request) {
	comms := s.node.GetJoinedCommunities()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(comms)
}

func (s *Server) handleJoinCommunity(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.validateToken(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CommunityID string `json:"community_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	communityID := models.CommunityID(req.CommunityID)

	// If the user is already in the community, check ban before returning.
	manager, _ := s.node.GetCommunity(communityID)
	if manager != nil {
		if isBanned(manager.GetModerationLog(), userID) {
			http.Error(w, "You are banned from this community", http.StatusForbidden)
			return
		}
		// Already a member — idempotent, return success.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Decide: bootstrap a new Raft cluster, or join as a follower.
	// HasRemoteCommunity is true when another node's CommunityAnnounce has been received,
	// meaning a leader already exists — this node must NOT bootstrap a second cluster.
	var joinErr error
	if s.node.HasRemoteCommunity(communityID) {
		joinErr = s.node.JoinCommunityAsFollower(communityID)
	} else {
		joinErr = s.node.JoinCommunity(communityID)
	}

	if joinErr != nil {
		if strings.Contains(joinErr.Error(), "already a member") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, joinErr.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetPosts(w http.ResponseWriter, r *http.Request) {
	commID := r.URL.Query().Get("community_id")
	manager, err := s.node.GetCommunity(models.CommunityID(commID))
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

	// Scan the moderation log once to build maps of removed posts and banned users.
	deletedPosts := make(map[models.ContentHash]bool)
	bannedUsers := make(map[string]bool)

	for _, entry := range manager.GetModerationLog() {
		switch entry.ActionType {
		case models.ModRemovePost:
			deletedPosts[entry.TargetHash] = true
		case models.ModRestorePost:
			delete(deletedPosts, entry.TargetHash)
		case models.ModBanUser:
			bannedUsers[string(entry.TargetUser)] = true
		case models.ModUnbanUser:
			delete(bannedUsers, string(entry.TargetUser))
		}
	}

	posts, scores := manager.GetPosts()
	type PostResponse struct {
		*models.Post
		Score int64 `json:"score"`
	}

	var res []PostResponse
	for _, p := range posts {
		// NEW: Only add the post to the feed if it is NOT deleted AND the author is NOT banned
		if !deletedPosts[p.Hash] && !bannedUsers[string(p.AuthorID)] {
			res = append(res, PostResponse{Post: p, Score: scores[p.Hash]})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.validateToken(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var post models.Post
	json.NewDecoder(r.Body).Decode(&post)
	post.AuthorID = models.UserID(userID) // always use server-validated identity

	manager, err := s.node.GetCommunity(post.CommunityID)
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

	if isBanned(manager.GetModerationLog(), userID) {
		http.Error(w, "You are banned from posting in this community", http.StatusForbidden)
		return
	}

	post.CreatedAt = time.Now()
	post.Hash = post.ComputeHash()

	hash, err := manager.CreatePost(&post)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"hash": string(hash)})
}

func (s *Server) handleGetComments(w http.ResponseWriter, r *http.Request) {
	commID := r.URL.Query().Get("community_id")
	postHash := r.URL.Query().Get("post_hash")

	manager, err := s.node.GetCommunity(models.CommunityID(commID))
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

	// Scan the moderation log to build the banned-users set and deleted comments.
	bannedUsers := make(map[string]bool)
	deletedComments := make(map[models.ContentHash]bool)
	for _, entry := range manager.GetModerationLog() {
		switch entry.ActionType {
		case models.ModRemoveComment:
			deletedComments[entry.TargetHash] = true
		case models.ModBanUser:
			bannedUsers[string(entry.TargetUser)] = true
		case models.ModUnbanUser:
			delete(bannedUsers, string(entry.TargetUser))
		}
	}

	comments, scores := manager.GetComments(models.ContentHash(postHash))
	type CommentResponse struct {
		*models.Comment
		Score int64 `json:"score"`
	}

	var res []CommentResponse
	for _, c := range comments {
		if !bannedUsers[string(c.AuthorID)] && !deletedComments[c.Hash] {
			res = append(res, CommentResponse{Comment: c, Score: scores[c.Hash]})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.validateToken(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CommunityID string         `json:"community_id"`
		Comment     models.Comment `json:"comment"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	req.Comment.AuthorID = models.UserID(userID) // always use server-validated identity

	manager, err := s.node.GetCommunity(models.CommunityID(req.CommunityID))
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

	if isBanned(manager.GetModerationLog(), userID) {
		http.Error(w, "You are banned from commenting in this community", http.StatusForbidden)
		return
	}

	req.Comment.CreatedAt = time.Now()
	req.Comment.Hash = req.Comment.ComputeHash()

	hash, err := manager.CreateComment(&req.Comment)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"hash": string(hash)})
}

func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.validateToken(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CommunityID string      `json:"community_id"`
		Vote        models.Vote `json:"vote"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	req.Vote.UserID = models.UserID(userID) // always use server-validated identity

	manager, err := s.node.GetCommunity(models.CommunityID(req.CommunityID))
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

	if isBanned(manager.GetModerationLog(), userID) {
		http.Error(w, "You are banned from voting in this community", http.StatusForbidden)
		return
	}

	req.Vote.Timestamp = time.Now()

	if err := manager.Vote(req.Vote); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleModerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := s.validateToken(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CommunityID string `json:"community_id"`
		ActionType  string `json:"action_type"`
		Target      string `json:"target"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	manager, err := s.node.GetCommunity(models.CommunityID(req.CommunityID))
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

	// Authorization guard (compares against raw UI action type strings)
	if userID != "admin" {
		if req.ActionType == "BAN_USER" {
			http.Error(w, "Forbidden: Only admins can ban users", http.StatusForbidden)
			return
		}
		if req.ActionType == "DELETE_POST" {
			// Non-admins can only delete their own posts
			posts, _ := manager.GetPosts()
			isAuthor := false
			for _, p := range posts {
				if string(p.Hash) == req.Target && string(p.AuthorID) == userID {
					isAuthor = true
					break
				}
			}
			if !isAuthor {
				http.Error(w, "Forbidden: You can only delete your own posts", http.StatusForbidden)
				return
			}
		}
	}

	// Map UI action type strings to model constants and route target to the correct field.
	action := models.ModerationAction{
		CommunityID: models.CommunityID(req.CommunityID),
		ModeratorID: models.UserID(userID),
		Reason:      "Moderated via API",
	}
	switch req.ActionType {
	case "DELETE_POST":
		action.ActionType = models.ModRemovePost
		action.TargetHash = models.ContentHash(req.Target)
	case "RESTORE_POST":
		action.ActionType = models.ModRestorePost
		action.TargetHash = models.ContentHash(req.Target)
	case "DELETE_COMMENT":
		action.ActionType = models.ModRemoveComment
		action.TargetHash = models.ContentHash(req.Target)
	case "BAN_USER":
		action.ActionType = models.ModBanUser
		action.TargetUser = models.UserID(req.Target)
	case "UNBAN_USER":
		action.ActionType = models.ModUnbanUser
		action.TargetUser = models.UserID(req.Target)
	default:
		http.Error(w, "Unknown action type: "+req.ActionType, http.StatusBadRequest)
		return
	}

	if err := manager.Moderate(action); err != nil {
		http.Error(w, "Raft consensus failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
