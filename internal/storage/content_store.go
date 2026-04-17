package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/crdt"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
)

type ContentStore struct {
	mu sync.RWMutex

	posts    map[models.ContentHash]*models.Post
	comments map[models.ContentHash]*models.Comment

	communityPosts map[models.CommunityID][]models.ContentHash
	postComments   map[models.ContentHash][]models.ContentHash // post hash → comment hashes

	votes map[models.ContentHash]*crdt.VoteState

	dataDir string
}

func NewContentStore(dataDir string) (*ContentStore, error) {
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create data dir: %w", err)
		}
	}

	cs := &ContentStore{
		posts:          make(map[models.ContentHash]*models.Post),
		comments:       make(map[models.ContentHash]*models.Comment),
		communityPosts: make(map[models.CommunityID][]models.ContentHash),
		postComments:   make(map[models.ContentHash][]models.ContentHash),
		votes:          make(map[models.ContentHash]*crdt.VoteState),
		dataDir:        dataDir,
	}

	if dataDir != "" {
		_ = cs.LoadFromDisk()
	}

	return cs, nil
}

func (cs *ContentStore) StorePost(post *models.Post) (models.ContentHash, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	hash := post.ComputeHash()
	post.Hash = hash

	cs.posts[hash] = post
	cs.communityPosts[post.CommunityID] = append(cs.communityPosts[post.CommunityID], hash)
	
	if _, exists := cs.votes[hash]; !exists {
		cs.votes[hash] = crdt.NewVoteState(hash)
		cs.persistVoteState(cs.votes[hash])
	}

	cs.persistPost(post)
	return hash, nil
}

func (cs *ContentStore) GetPost(hash models.ContentHash) (*models.Post, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	post, ok := cs.posts[hash]
	if !ok {
		return nil, fmt.Errorf("post not found: %s", hash)
	}
	return post, nil
}

func (cs *ContentStore) GetCommunityPosts(communityID models.CommunityID) []models.ContentHash {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	hashes := cs.communityPosts[communityID]
	result := make([]models.ContentHash, len(hashes))
	copy(result, hashes)
	return result
}

func (cs *ContentStore) StoreComment(comment *models.Comment) (models.ContentHash, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	hash := comment.ComputeHash()
	comment.Hash = hash

	cs.comments[hash] = comment
	cs.postComments[comment.PostHash] = append(cs.postComments[comment.PostHash], hash)
	
	if _, exists := cs.votes[hash]; !exists {
		cs.votes[hash] = crdt.NewVoteState(hash)
		cs.persistVoteState(cs.votes[hash])
	}

	cs.persistComment(comment)
	return hash, nil
}

func (cs *ContentStore) GetComment(hash models.ContentHash) (*models.Comment, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	comment, ok := cs.comments[hash]
	if !ok {
		return nil, fmt.Errorf("comment not found: %s", hash)
	}
	return comment, nil
}

func (cs *ContentStore) GetPostComments(postHash models.ContentHash) []models.ContentHash {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	hashes := cs.postComments[postHash]
	result := make([]models.ContentHash, len(hashes))
	copy(result, hashes)
	return result
}

func (cs *ContentStore) ApplyVote(vote models.Vote, nodeID models.NodeID) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	vs, ok := cs.votes[vote.TargetHash]
	if !ok {
		return fmt.Errorf("target not found: %s", vote.TargetHash)
	}

	vs.ApplyVote(vote, nodeID)
	cs.persistVoteState(vs) // Save the new score to disk instantly
	return nil
}

func (cs *ContentStore) GetVoteScore(hash models.ContentHash) (int64, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	vs, ok := cs.votes[hash]
	if !ok {
		return 0, fmt.Errorf("vote state not found: %s", hash)
	}
	return vs.GetScore(), nil
}

// ---------------------------------------------------------
// Persistence Methods
// ---------------------------------------------------------

func (cs *ContentStore) persistPost(post *models.Post) {
	if cs.dataDir == "" {
		return
	}
	dir := filepath.Join(cs.dataDir, "posts")
	_ = os.MkdirAll(dir, 0755)

	data, err := json.MarshalIndent(post, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, string(post.Hash)+".json"), data, 0644)
}

func (cs *ContentStore) persistComment(comment *models.Comment) {
	if cs.dataDir == "" {
		return
	}
	dir := filepath.Join(cs.dataDir, "comments")
	_ = os.MkdirAll(dir, 0755)

	data, err := json.MarshalIndent(comment, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, string(comment.Hash)+".json"), data, 0644)
}

func (cs *ContentStore) persistVoteState(vs *crdt.VoteState) {
	if cs.dataDir == "" || vs == nil {
		return
	}
	dir := filepath.Join(cs.dataDir, "votes")
	_ = os.MkdirAll(dir, 0755)

	data, err := json.MarshalIndent(vs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, string(vs.TargetHash)+".json"), data, 0644)
}

func (cs *ContentStore) LoadFromDisk() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// 1. Load Posts
	postsDir := filepath.Join(cs.dataDir, "posts")
	if entries, err := os.ReadDir(postsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(postsDir, entry.Name()))
			if err != nil {
				continue
			}
			var post models.Post
			if err := json.Unmarshal(data, &post); err == nil {
				cs.posts[post.Hash] = &post
				cs.communityPosts[post.CommunityID] = append(cs.communityPosts[post.CommunityID], post.Hash)
				if _, exists := cs.votes[post.Hash]; !exists {
					cs.votes[post.Hash] = crdt.NewVoteState(post.Hash)
				}
			}
		}
	}

	// 2. Load Comments
	commentsDir := filepath.Join(cs.dataDir, "comments")
	if entries, err := os.ReadDir(commentsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(commentsDir, entry.Name()))
			if err != nil {
				continue
			}
			var comment models.Comment
			if err := json.Unmarshal(data, &comment); err == nil {
				cs.comments[comment.Hash] = &comment
				cs.postComments[comment.PostHash] = append(cs.postComments[comment.PostHash], comment.Hash)
				if _, exists := cs.votes[comment.Hash]; !exists {
					cs.votes[comment.Hash] = crdt.NewVoteState(comment.Hash)
				}
			}
		}
	}

	// 3. Load Vote States (Overwrites the empty ones created above)
	votesDir := filepath.Join(cs.dataDir, "votes")
	if entries, err := os.ReadDir(votesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(votesDir, entry.Name()))
			if err != nil {
				continue
			}
			var vs crdt.VoteState
			if err := json.Unmarshal(data, &vs); err == nil {
				cs.votes[vs.TargetHash] = &vs
			}
		}
	}

	return nil
}

// ---------------------------------------------------------
// Helper / Utility Methods
// ---------------------------------------------------------

func (cs *ContentStore) HasPost(hash models.ContentHash) bool {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	_, ok := cs.posts[hash]
	return ok
}

func (cs *ContentStore) HasComment(hash models.ContentHash) bool {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	_, ok := cs.comments[hash]
	return ok
}

func (cs *ContentStore) GetAllPosts() []*models.Post {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	posts := make([]*models.Post, 0, len(cs.posts))
	for _, post := range cs.posts {
		posts = append(posts, post)
	}
	return posts
}

func (cs *ContentStore) GetAllComments() []*models.Comment {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	comments := make([]*models.Comment, 0, len(cs.comments))
	for _, comment := range cs.comments {
		comments = append(comments, comment)
	}
	return comments
}

func (cs *ContentStore) GetVoteState(hash models.ContentHash) *crdt.VoteState {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	vs, ok := cs.votes[hash]
	if !ok {
		return nil
	}
	return vs.Clone()
}

func (cs *ContentStore) MergeVoteState(incoming *crdt.VoteState) error {
	if incoming == nil {
		return nil
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	local, ok := cs.votes[incoming.TargetHash]
	if !ok {
		cs.votes[incoming.TargetHash] = incoming.Clone()
		cs.persistVoteState(cs.votes[incoming.TargetHash]) // Persist the merged data
		return nil
	}

	local.Merge(incoming)
	cs.persistVoteState(local) // Persist the updated data
	return nil
}

func (cs *ContentStore) GetAllVoteStates() []*crdt.VoteState {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	states := make([]*crdt.VoteState, 0, len(cs.votes))
	for _, vs := range cs.votes {
		states = append(states, vs.Clone())
	}
	return states
}