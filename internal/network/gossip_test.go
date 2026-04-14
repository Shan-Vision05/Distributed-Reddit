package network

import (
"fmt"
"testing"
"time"

"github.com/Shan-Vision05/Distributed-Reddit/internal/crdt"
"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
"github.com/Shan-Vision05/Distributed-Reddit/internal/storage"
)

func createTestNode(t *testing.T, nodeID string, port int) (*GossipNode, *storage.ContentStore) {
t.Helper()

store, err := storage.NewContentStore("")
if err != nil {
t.Fatalf("failed to create store: %v", err)
}

cfg := GossipConfig{
NodeID:   models.NodeID(nodeID),
BindAddr: "127.0.0.1",
BindPort: port,
}

node, err := NewGossipNode(cfg, store)
if err != nil {
t.Fatalf("failed to create gossip node: %v", err)
}

return node, store
}

func TestGossipNode_Create(t *testing.T) {
node, _ := createTestNode(t, "node1", 17946)
defer node.Shutdown()

if node.NumMembers() != 1 {
t.Errorf("expected 1 member (self), got %d", node.NumMembers())
}

if node.LocalNode().Name != "node1" {
t.Errorf("expected node name 'node1', got '%s'", node.LocalNode().Name)
}
}

func TestGossipNode_JoinCluster(t *testing.T) {
node1, _ := createTestNode(t, "node1", 17947)
defer node1.Shutdown()

node2, _ := createTestNode(t, "node2", 17948)
defer node2.Shutdown()

n, err := node2.Join([]string{"127.0.0.1:17947"})
if err != nil {
t.Fatalf("failed to join cluster: %v", err)
}
if n != 1 {
t.Errorf("expected to join 1 node, joined %d", n)
}

time.Sleep(100 * time.Millisecond)

if node1.NumMembers() != 2 {
t.Errorf("node1: expected 2 members, got %d", node1.NumMembers())
}
if node2.NumMembers() != 2 {
t.Errorf("node2: expected 2 members, got %d", node2.NumMembers())
}
}

func TestGossipNode_ThreeNodeCluster(t *testing.T) {
node1, _ := createTestNode(t, "node1", 17949)
defer node1.Shutdown()

node2, _ := createTestNode(t, "node2", 17950)
defer node2.Shutdown()

node3, _ := createTestNode(t, "node3", 17951)
defer node3.Shutdown()

_, err := node2.Join([]string{"127.0.0.1:17949"})
if err != nil {
t.Fatalf("node2 failed to join: %v", err)
}

_, err = node3.Join([]string{"127.0.0.1:17949"})
if err != nil {
t.Fatalf("node3 failed to join: %v", err)
}

time.Sleep(200 * time.Millisecond)

for i, node := range []*GossipNode{node1, node2, node3} {
if node.NumMembers() != 3 {
t.Errorf("node%d: expected 3 members, got %d", i+1, node.NumMembers())
}
}
}

func TestGossipNode_PeerJoinCallback(t *testing.T) {
joinCalled := make(chan models.NodeID, 1)

node1, _ := createTestNode(t, "node1", 17952)
defer node1.Shutdown()

node1.SetOnPeerJoin(func(id models.NodeID, addr string) {
joinCalled <- id
})

node2, _ := createTestNode(t, "node2", 17953)
defer node2.Shutdown()

_, err := node2.Join([]string{"127.0.0.1:17952"})
if err != nil {
t.Fatalf("failed to join: %v", err)
}

select {
case id := <-joinCalled:
if id != "node2" {
t.Errorf("expected join callback for 'node2', got '%s'", id)
}
case <-time.After(500 * time.Millisecond):
t.Error("join callback not called")
}
}

func TestGossipNode_BroadcastPost(t *testing.T) {
node1, store1 := createTestNode(t, "node1", 17954)
defer node1.Shutdown()

node2, store2 := createTestNode(t, "node2", 17955)
defer node2.Shutdown()

_, err := node2.Join([]string{"127.0.0.1:17954"})
if err != nil {
t.Fatalf("failed to join: %v", err)
}
time.Sleep(100 * time.Millisecond)

post := &models.Post{
CommunityID: "golang",
AuthorID:    "user1",
Title:       "Test Post",
Body:        "This is a test post",
CreatedAt:   time.Now(),
}
hash, err := store1.StorePost(post)
if err != nil {
t.Fatalf("failed to store post: %v", err)
}

err = node1.BroadcastPost(post)
if err != nil {
t.Fatalf("failed to broadcast post: %v", err)
}

time.Sleep(300 * time.Millisecond)

receivedPost, err := store2.GetPost(hash)
if err != nil {
t.Errorf("node2 did not receive post: %v", err)
} else if receivedPost.Title != "Test Post" {
t.Errorf("received post title mismatch: got '%s'", receivedPost.Title)
}
}

func TestGossipNode_BroadcastComment(t *testing.T) {
node1, store1 := createTestNode(t, "node1", 17956)
defer node1.Shutdown()

node2, store2 := createTestNode(t, "node2", 17957)
defer node2.Shutdown()

_, err := node2.Join([]string{"127.0.0.1:17956"})
if err != nil {
t.Fatalf("failed to join: %v", err)
}
time.Sleep(100 * time.Millisecond)

post := &models.Post{
CommunityID: "golang",
AuthorID:    "user1",
Title:       "Test Post",
Body:        "Body",
CreatedAt:   time.Now(),
}
postHash, _ := store1.StorePost(post)
store2.StorePost(post)

comment := &models.Comment{
PostHash:  postHash,
AuthorID:  "user2",
Body:      "Great post!",
CreatedAt: time.Now(),
}
commentHash, _ := store1.StoreComment(comment)

err = node1.BroadcastComment(comment)
if err != nil {
t.Fatalf("failed to broadcast comment: %v", err)
}

time.Sleep(300 * time.Millisecond)

receivedComment, err := store2.GetComment(commentHash)
if err != nil {
t.Errorf("node2 did not receive comment: %v", err)
} else if receivedComment.Body != "Great post!" {
t.Errorf("received comment body mismatch: got '%s'", receivedComment.Body)
}
}

func TestGossipNode_BroadcastVoteState(t *testing.T) {
node1, store1 := createTestNode(t, "node1", 17958)
defer node1.Shutdown()

node2, store2 := createTestNode(t, "node2", 17959)
defer node2.Shutdown()

_, err := node2.Join([]string{"127.0.0.1:17958"})
if err != nil {
t.Fatalf("failed to join: %v", err)
}
time.Sleep(100 * time.Millisecond)

post := &models.Post{
CommunityID: "golang",
AuthorID:    "user1",
Title:       "Vote Test",
Body:        "Body",
CreatedAt:   time.Now(),
}
postHash, _ := store1.StorePost(post)
store2.StorePost(post)

vote := models.Vote{
TargetHash: postHash,
UserID:     "voter1",
Value:      models.Upvote,
Timestamp:  time.Now(),
}
store1.ApplyVote(vote, "node1")

vs := store1.GetVoteState(postHash)
err = node1.BroadcastVoteState(vs)
if err != nil {
t.Fatalf("failed to broadcast vote state: %v", err)
}

time.Sleep(300 * time.Millisecond)

score, err := store2.GetVoteScore(postHash)
if err != nil {
t.Errorf("failed to get vote score on node2: %v", err)
} else if score != 1 {
t.Errorf("expected score 1, got %d", score)
}
}

func TestGossipNode_VoteStateMerge(t *testing.T) {
node1, store1 := createTestNode(t, "node1", 17960)
defer node1.Shutdown()

node2, store2 := createTestNode(t, "node2", 17961)
defer node2.Shutdown()

_, err := node2.Join([]string{"127.0.0.1:17960"})
if err != nil {
t.Fatalf("failed to join: %v", err)
}
time.Sleep(100 * time.Millisecond)

post := &models.Post{
CommunityID: "golang",
AuthorID:    "user1",
Title:       "Merge Test",
Body:        "Body",
CreatedAt:   time.Now(),
}
postHash, _ := store1.StorePost(post)
store2.StorePost(post)

vote1 := models.Vote{
TargetHash: postHash,
UserID:     "voter1",
Value:      models.Upvote,
Timestamp:  time.Now(),
}
store1.ApplyVote(vote1, "node1")

vote2 := models.Vote{
TargetHash: postHash,
UserID:     "voter2",
Value:      models.Upvote,
Timestamp:  time.Now(),
}
store2.ApplyVote(vote2, "node2")

vs1 := store1.GetVoteState(postHash)
vs2 := store2.GetVoteState(postHash)

node1.BroadcastVoteState(vs1)
node2.BroadcastVoteState(vs2)

time.Sleep(300 * time.Millisecond)

score1, _ := store1.GetVoteScore(postHash)
score2, _ := store2.GetVoteScore(postHash)

if score1 != 2 {
t.Errorf("node1: expected score 2, got %d", score1)
}
if score2 != 2 {
t.Errorf("node2: expected score 2, got %d", score2)
}
}

func TestGossipNode_IdempotentPostDelivery(t *testing.T) {
node1, store1 := createTestNode(t, "node1", 17962)
defer node1.Shutdown()

node2, store2 := createTestNode(t, "node2", 17963)
defer node2.Shutdown()

_, _ = node2.Join([]string{"127.0.0.1:17962"})
time.Sleep(100 * time.Millisecond)

post := &models.Post{
CommunityID: "golang",
AuthorID:    "user1",
Title:       "Idempotent Test",
Body:        "Body",
CreatedAt:   time.Now(),
}
store1.StorePost(post)

node1.BroadcastPost(post)
node1.BroadcastPost(post)
node1.BroadcastPost(post)

time.Sleep(300 * time.Millisecond)

posts := store2.GetAllPosts()
if len(posts) != 1 {
t.Errorf("expected 1 post (idempotent), got %d", len(posts))
}
}

func TestGossipNode_ContentStore_Integration(t *testing.T) {
node1, store1 := createTestNode(t, "node1", 17966)
defer node1.Shutdown()

node2, store2 := createTestNode(t, "node2", 17967)
defer node2.Shutdown()

node3, store3 := createTestNode(t, "node3", 17968)
defer node3.Shutdown()

node2.Join([]string{"127.0.0.1:17966"})
node3.Join([]string{"127.0.0.1:17966"})
time.Sleep(200 * time.Millisecond)

post := &models.Post{
CommunityID: "test",
AuthorID:    "author",
Title:       "Integration Test",
Body:        "Testing full integration",
CreatedAt:   time.Now(),
}
postHash, _ := store1.StorePost(post)
node1.BroadcastPost(post)

store2.StorePost(post)
store3.StorePost(post)

comment := &models.Comment{
PostHash:  postHash,
AuthorID:  "commenter",
Body:      "Nice!",
CreatedAt: time.Now(),
}
commentHash, _ := store1.StoreComment(comment)
node1.BroadcastComment(comment)

time.Sleep(300 * time.Millisecond)

for i, store := range []*storage.ContentStore{store1, store2, store3} {
p, err := store.GetPost(postHash)
if err != nil {
t.Errorf("store%d: missing post: %v", i+1, err)
} else if p.Title != "Integration Test" {
t.Errorf("store%d: wrong post title", i+1)
}

c, err := store.GetComment(commentHash)
if err != nil {
t.Errorf("store%d: missing comment: %v", i+1, err)
} else if c.Body != "Nice!" {
t.Errorf("store%d: wrong comment body", i+1)
}
}
}

func TestVoteState_Merge(t *testing.T) {
hash := models.ContentHash("test-hash")

vs1 := crdt.NewVoteState(hash)
vs2 := crdt.NewVoteState(hash)

vs1.ApplyVote(models.Vote{
TargetHash: hash,
UserID:     "user1",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, "node1")

vs2.ApplyVote(models.Vote{
TargetHash: hash,
UserID:     "user2",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, "node2")

vs1.Merge(vs2)

score := vs1.GetScore()
if score != 2 {
t.Errorf("expected merged score 2, got %d", score)
}
}

func TestVoteState_Clone(t *testing.T) {
hash := models.ContentHash("test-hash")
vs := crdt.NewVoteState(hash)

vs.ApplyVote(models.Vote{
TargetHash: hash,
UserID:     "user1",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, "node1")

clone := vs.Clone()

vs.ApplyVote(models.Vote{
TargetHash: hash,
UserID:     "user2",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, "node1")

if clone.GetScore() != 1 {
t.Errorf("clone should have score 1, got %d", clone.GetScore())
}
if vs.GetScore() != 2 {
t.Errorf("original should have score 2, got %d", vs.GetScore())
}
}

func TestContentStore_NewMethods(t *testing.T) {
store, _ := storage.NewContentStore("")

post := &models.Post{
CommunityID: "test",
AuthorID:    "author",
Title:       "Test",
Body:        "Body",
CreatedAt:   time.Now(),
}
hash, _ := store.StorePost(post)

if !store.HasPost(hash) {
t.Error("HasPost should return true for stored post")
}
if store.HasPost("nonexistent") {
t.Error("HasPost should return false for nonexistent post")
}

posts := store.GetAllPosts()
if len(posts) != 1 {
t.Errorf("expected 1 post, got %d", len(posts))
}

vs := store.GetVoteState(hash)
if vs == nil {
t.Error("GetVoteState should return non-nil for stored post")
}

incoming := crdt.NewVoteState(hash)
incoming.ApplyVote(models.Vote{
TargetHash: hash,
UserID:     "external",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, "external-node")

err := store.MergeVoteState(incoming)
if err != nil {
t.Errorf("MergeVoteState failed: %v", err)
}

score, _ := store.GetVoteScore(hash)
if score != 1 {
t.Errorf("expected score 1 after merge, got %d", score)
}
}

func TestGossipNode_MultiplePostsBroadcast(t *testing.T) {
node1, store1 := createTestNode(t, "node1", 17969)
defer node1.Shutdown()

node2, store2 := createTestNode(t, "node2", 17970)
defer node2.Shutdown()

node2.Join([]string{"127.0.0.1:17969"})
time.Sleep(100 * time.Millisecond)

for i := 0; i < 5; i++ {
post := &models.Post{
CommunityID: "test",
AuthorID:    "author",
Title:       fmt.Sprintf("Post %d", i),
Body:        "Body",
CreatedAt:   time.Now().Add(time.Duration(i) * time.Millisecond),
}
store1.StorePost(post)
node1.BroadcastPost(post)
}

time.Sleep(500 * time.Millisecond)

posts := store2.GetAllPosts()
if len(posts) != 5 {
t.Errorf("expected 5 posts, got %d", len(posts))
}
}
