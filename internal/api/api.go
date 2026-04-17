package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/node"
)

type Server struct {
	node     *node.Node
	mu       sync.RWMutex
	users    map[string]string // Maps username -> hashed password
	authFile string
}

func NewServer(n *node.Node) *Server {
	s := &Server{
		node:     n,
		users:    make(map[string]string),
		authFile: "users.json",
	}
	s.loadUsers()
	return s
}

// --- User Account Management ---

func (s *Server) loadUsers() {
	data, err := os.ReadFile(s.authFile)
	if err == nil {
		json.Unmarshal(data, &s.users)
	}
}

func (s *Server) saveUsers() {
	data, _ := json.MarshalIndent(s.users, "", "  ")
	_ = os.WriteFile(s.authFile, data, 0644)
}

func hashPassword(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
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

	s.users[req.Username] = hashPassword(req.Password)
	s.saveUsers()
	w.WriteHeader(http.StatusCreated)
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

	s.mu.RLock()
	defer s.mu.RUnlock()

	expectedHash, exists := s.users[req.Username]
	if !exists || expectedHash != hashPassword(req.Password) {
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// --- Main Server Setup ---

func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	mux.Handle("/", http.FileServer(http.Dir("./ui")))

	// Auth Endpoints
	mux.HandleFunc("/api/signup", s.handleSignup)
	mux.HandleFunc("/api/login", s.handleLogin)

	// App Endpoints
	mux.HandleFunc("/api/communities", s.handleGetCommunities)
	mux.HandleFunc("/api/join", s.handleJoinCommunity)
	mux.HandleFunc("/api/posts", s.handleGetPosts)
	mux.HandleFunc("/api/post", s.handleCreatePost)
	mux.HandleFunc("/api/comments", s.handleGetComments)
	mux.HandleFunc("/api/comment", s.handleCreateComment)
	mux.HandleFunc("/api/vote", s.handleVote)

	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleGetCommunities(w http.ResponseWriter, r *http.Request) {
	comms := s.node.GetJoinedCommunities()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(comms)
}

func (s *Server) handleJoinCommunity(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CommunityID string `json:"community_id"`
		UserID      string `json:"user_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.UserID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := s.node.JoinCommunity(models.CommunityID(req.CommunityID)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	posts, scores := manager.GetPosts()
	type PostResponse struct {
		*models.Post
		Score int64 `json:"score"`
	}
	
	var res []PostResponse
	for _, p := range posts {
		res = append(res, PostResponse{Post: p, Score: scores[p.Hash]})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	var post models.Post
	json.NewDecoder(r.Body).Decode(&post)
	
	if post.AuthorID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	post.CreatedAt = time.Now()
	post.Hash = post.ComputeHash()

	manager, err := s.node.GetCommunity(post.CommunityID)
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

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

	comments, scores := manager.GetComments(models.ContentHash(postHash))
	type CommentResponse struct {
		*models.Comment
		Score int64 `json:"score"`
	}

	var res []CommentResponse
	for _, c := range comments {
		res = append(res, CommentResponse{Comment: c, Score: scores[c.Hash]})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CommunityID string         `json:"community_id"`
		Comment     models.Comment `json:"comment"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Comment.AuthorID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	req.Comment.CreatedAt = time.Now()
	req.Comment.Hash = req.Comment.ComputeHash()

	manager, err := s.node.GetCommunity(models.CommunityID(req.CommunityID))
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

	hash, err := manager.CreateComment(&req.Comment)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"hash": string(hash)})
}

func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CommunityID string      `json:"community_id"`
		Vote        models.Vote `json:"vote"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Vote.UserID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	req.Vote.Timestamp = time.Now()

	manager, err := s.node.GetCommunity(models.CommunityID(req.CommunityID))
	if err != nil {
		http.Error(w, "Not a member", http.StatusNotFound)
		return
	}

	if err := manager.Vote(req.Vote); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}