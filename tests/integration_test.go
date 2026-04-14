package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/crdt"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/dht"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/network"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/storage"
)

// =====================================
// Integration Tests: DHT + Storage
// =====================================

// Simulates a multi-node cluster where each node has its own ContentStore
// and the DHT determines which nodes store which community's content.
func TestDHTRoutesContentToCorrectNodes(t *testing.T) {
// 1. Set up DHT with 3 nodes, replication=2
d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 2})
nodeIDs := []models.NodeID{"node-alpha", "node-beta", "node-gamma"}
for _, id := range nodeIDs {
d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
}

// 2. Each node has its own ContentStore
stores := make(map[models.NodeID]*storage.ContentStore)
for _, id := range nodeIDs {
cs, err := storage.NewContentStore("")
if err != nil {
t.Fatalf("failed to create store for %s: %v", id, err)
}
stores[id] = cs
}

// 3. Create posts in different communities
communities := []models.CommunityID{"golang", "rust", "python", "javascript"}

for _, communityID := range communities {
// DHT tells us which nodes are responsible
responsible := d.LookupNodes(communityID)
if len(responsible) != 2 {
t.Fatalf("expected 2 responsible nodes for %s, got %d", communityID, len(responsible))
}

post := &models.Post{
CommunityID: communityID,
AuthorID:    "user-1",
Title:       fmt.Sprintf("Post in %s", communityID),
Body:        "Test content",
CreatedAt:   time.Now(),
}

// Store on responsible nodes only
for _, nodeID := range responsible {
_, err := stores[nodeID].StorePost(post)
if err != nil {
t.Fatalf("failed to store post on %s: %v", nodeID, err)
}
}
}

// 4. Verify: each community's posts exist only on responsible nodes
for _, communityID := range communities {
responsible := d.LookupNodes(communityID)
responsibleSet := make(map[models.NodeID]bool)
for _, n := range responsible {
responsibleSet[n] = true
}

for _, nodeID := range nodeIDs {
posts := stores[nodeID].GetCommunityPosts(communityID)
if responsibleSet[nodeID] {
if len(posts) == 0 {
t.Errorf("node %s should have posts for %s but doesn't", nodeID, communityID)
}
} else {
if len(posts) > 0 {
t.Errorf("node %s should NOT have posts for %s but does", nodeID, communityID)
}
}
}
}
}

// Tests that when a node is removed, its communities get reassigned and
// content can be replicated to the new responsible node.
func TestDHTNodeFailoverReplication(t *testing.T) {
d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 2})
nodeIDs := []models.NodeID{"node-1", "node-2", "node-3", "node-4"}
stores := make(map[models.NodeID]*storage.ContentStore)

for _, id := range nodeIDs {
d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[id] = cs
}

communityID := models.CommunityID("golang")

// Store a post on current responsible nodes
originalNodes := d.LookupNodes(communityID)
post := &models.Post{
CommunityID: communityID,
AuthorID:    "user-1",
Title:       "Go is great",
Body:        "Discussion thread",
CreatedAt:   time.Now(),
}

var postHash models.ContentHash
for _, nodeID := range originalNodes {
h, _ := stores[nodeID].StorePost(post)
postHash = h
}

// Remove one responsible node (simulating failure)
failedNode := originalNodes[0]
d.RemoveNode(failedNode)

// New responsible nodes after failure
newNodes := d.LookupNodes(communityID)

// Replicate content to any NEW node that wasn't previously responsible
originalSet := make(map[models.NodeID]bool)
for _, n := range originalNodes {
originalSet[n] = true
}

for _, nodeID := range newNodes {
if !originalSet[nodeID] {
// This is a new responsible node - replicate from a surviving node
var sourceNode models.NodeID
for _, orig := range originalNodes {
if orig != failedNode {
sourceNode = orig
break
}
}

// Copy content from source
srcPost, err := stores[sourceNode].GetPost(postHash)
if err != nil {
t.Fatalf("source node %s lost the post: %v", sourceNode, err)
}
stores[nodeID].StorePost(srcPost)
}
}

// Verify all new responsible nodes have the content
for _, nodeID := range newNodes {
if !stores[nodeID].HasPost(postHash) {
t.Errorf("node %s should have the replicated post after failover", nodeID)
}
}
}

// =====================================
// Integration Tests: DHT + CRDT + Storage
// =====================================

// Tests that votes on content are correctly merged across replicas
// determined by the DHT.
func TestDHTCRDTVoteSyncAcrossReplicas(t *testing.T) {
d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 3})
nodeIDs := []models.NodeID{"node-A", "node-B", "node-C", "node-D"}
stores := make(map[models.NodeID]*storage.ContentStore)

for _, id := range nodeIDs {
d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[id] = cs
}

communityID := models.CommunityID("golang")
responsible := d.LookupNodes(communityID)

// Create a post on all responsible nodes
post := &models.Post{
CommunityID: communityID,
AuthorID:    "author-1",
Title:       "Best Go pattern?",
Body:        "What's your favorite pattern in Go?",
CreatedAt:   time.Now(),
}

var postHash models.ContentHash
for _, nodeID := range responsible {
h, _ := stores[nodeID].StorePost(post)
postHash = h
}

// Users vote on different nodes (simulating concurrent operations)
// User-1 upvotes on node A
stores[responsible[0]].ApplyVote(models.Vote{
TargetHash: postHash,
UserID:     "user-1",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, responsible[0])

// User-2 upvotes on node B
stores[responsible[1]].ApplyVote(models.Vote{
TargetHash: postHash,
UserID:     "user-2",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, responsible[1])

// User-3 downvotes on node C (or last responsible)
lastNode := responsible[len(responsible)-1]
stores[lastNode].ApplyVote(models.Vote{
TargetHash: postHash,
UserID:     "user-3",
Value:      models.Downvote,
Timestamp:  time.Now(),
}, lastNode)

// Before merge: each node only sees its local votes
score0, _ := stores[responsible[0]].GetVoteScore(postHash)
if score0 != 1 {
t.Errorf("node %s: expected score 1 before merge, got %d", responsible[0], score0)
}

// CRDT merge: sync vote states across all replicas
// Collect all vote states
voteStates := make(map[models.NodeID]*crdt.VoteState)
for _, nodeID := range responsible {
vs := stores[nodeID].GetVoteState(postHash)
if vs == nil {
t.Fatalf("node %s has nil vote state", nodeID)
}
voteStates[nodeID] = vs
}

// Merge each node's state into every other node
for _, nodeID := range responsible {
for _, otherID := range responsible {
if nodeID == otherID {
continue
}
stores[nodeID].MergeVoteState(voteStates[otherID])
}
}

// After merge: all nodes should agree on score = 2 upvotes - 1 downvote = 1
for _, nodeID := range responsible {
score, _ := stores[nodeID].GetVoteScore(postHash)
if score != 1 {
t.Errorf("node %s: expected merged score 1 (2up - 1down), got %d", nodeID, score)
}
}
}

// =====================================
// Integration Tests: DHT + Storage + Community Distribution
// =====================================

// End-to-end: multiple communities, multiple nodes, posts + comments + votes,
// DHT routing, CRDT merging.
func TestEndToEnd_MultiCommunityCluster(t *testing.T) {
// Cluster config
d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 100, ReplicationFactor: 2})
nodeIDs := []models.NodeID{"node-1", "node-2", "node-3", "node-4", "node-5"}
stores := make(map[models.NodeID]*storage.ContentStore)

for _, id := range nodeIDs {
d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[id] = cs
}

communities := []models.CommunityID{"golang", "rust", "python"}

type postRecord struct {
hash        models.ContentHash
community   models.CommunityID
responsible []models.NodeID
}

var allPosts []postRecord

// --- Phase 1: Create posts across communities ---
for _, communityID := range communities {
responsible := d.LookupNodes(communityID)

for i := 0; i < 3; i++ {
post := &models.Post{
CommunityID: communityID,
AuthorID:    models.UserID(fmt.Sprintf("author-%d", i)),
Title:       fmt.Sprintf("Post %d in %s", i, communityID),
Body:        fmt.Sprintf("Content for post %d", i),
CreatedAt:   time.Now().Add(time.Duration(i) * time.Second),
}

var hash models.ContentHash
for _, nodeID := range responsible {
h, err := stores[nodeID].StorePost(post)
if err != nil {
t.Fatalf("store post failed: %v", err)
}
hash = h
}

allPosts = append(allPosts, postRecord{
hash:        hash,
community:   communityID,
responsible: responsible,
})
}
}

// --- Phase 2: Add comments ---
for _, pr := range allPosts {
comment := &models.Comment{
PostHash:  pr.hash,
AuthorID:  "commenter-1",
Body:      fmt.Sprintf("Comment on %s", pr.hash),
CreatedAt: time.Now(),
}

for _, nodeID := range pr.responsible {
_, err := stores[nodeID].StoreComment(comment)
if err != nil {
t.Fatalf("store comment failed: %v", err)
}
}
}

// --- Phase 3: Vote on posts ---
for _, pr := range allPosts {
// Upvote from user-1 on first replica
stores[pr.responsible[0]].ApplyVote(models.Vote{
TargetHash: pr.hash,
UserID:     "user-1",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, pr.responsible[0])

// Upvote from user-2 on second replica
if len(pr.responsible) > 1 {
stores[pr.responsible[1]].ApplyVote(models.Vote{
TargetHash: pr.hash,
UserID:     "user-2",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, pr.responsible[1])
}
}

// --- Phase 4: Merge vote states across replicas ---
for _, pr := range allPosts {
voteStates := make(map[models.NodeID]*crdt.VoteState)
for _, nodeID := range pr.responsible {
vs := stores[nodeID].GetVoteState(pr.hash)
if vs != nil {
voteStates[nodeID] = vs
}
}

for _, nodeID := range pr.responsible {
for _, otherID := range pr.responsible {
if nodeID == otherID {
continue
}
if vs, ok := voteStates[otherID]; ok {
stores[nodeID].MergeVoteState(vs)
}
}
}
}

// --- Phase 5: Verify ---
for _, pr := range allPosts {
for _, nodeID := range pr.responsible {
// Post should exist
if !stores[nodeID].HasPost(pr.hash) {
t.Errorf("node %s missing post %s", nodeID, pr.hash)
}

// Comments should exist
comments := stores[nodeID].GetPostComments(pr.hash)
if len(comments) != 1 {
t.Errorf("node %s: expected 1 comment for %s, got %d", nodeID, pr.hash, len(comments))
}

// Vote score should be 2 (both upvotes merged)
score, err := stores[nodeID].GetVoteScore(pr.hash)
if err != nil {
t.Errorf("node %s: vote score error: %v", nodeID, err)
}
if score != 2 {
t.Errorf("node %s: expected merged score 2 for %s, got %d", nodeID, pr.hash, score)
}
}
}

// --- Phase 6: Check distribution across nodes ---
dist := d.GetDistribution(communities)
totalPrimaries := 0
for _, count := range dist {
totalPrimaries += count
}
if totalPrimaries != len(communities) {
t.Errorf("distribution should cover all %d communities, got %d", len(communities), totalPrimaries)
}
}

// Tests that adding a node to the cluster and rebalancing works end-to-end.
func TestEndToEnd_NodeJoinAndRebalance(t *testing.T) {
d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 2})
stores := make(map[models.NodeID]*storage.ContentStore)

// Start with 3 nodes
for i := 0; i < 3; i++ {
id := models.NodeID(fmt.Sprintf("node-%d", i))
d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[id] = cs
}

communities := []models.CommunityID{"golang", "rust", "python", "java", "haskell"}

// Populate content
postHashes := make(map[models.CommunityID]models.ContentHash)
for _, cid := range communities {
responsible := d.LookupNodes(cid)
post := &models.Post{
CommunityID: cid,
AuthorID:    "user-1",
Title:       fmt.Sprintf("Post in %s", cid),
Body:        "Content",
CreatedAt:   time.Now(),
}
for _, nodeID := range responsible {
h, _ := stores[nodeID].StorePost(post)
postHashes[cid] = h
}
}

// Record pre-add responsible nodes
preMappings := make(map[models.CommunityID][]models.NodeID)
for _, cid := range communities {
preMappings[cid] = d.LookupNodes(cid)
}

// Add a new node
newID := models.NodeID("node-3")
d.AddNode(&models.NodeInfo{ID: newID, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[newID] = cs

// Rebalance: for each community, if responsible nodes changed, replicate
rebalanced := 0
for _, cid := range communities {
newResponsible := d.LookupNodes(cid)
preSet := make(map[models.NodeID]bool)
for _, n := range preMappings[cid] {
preSet[n] = true
}

for _, nodeID := range newResponsible {
if !preSet[nodeID] {
// New node needs this community's data
// Find a source that has it
for _, srcNode := range preMappings[cid] {
hash := postHashes[cid]
if stores[srcNode].HasPost(hash) {
p, _ := stores[srcNode].GetPost(hash)
stores[nodeID].StorePost(p)
rebalanced++
break
}
}
}
}
}

// Verify: all current responsible nodes have the content
for _, cid := range communities {
responsible := d.LookupNodes(cid)
hash := postHashes[cid]
for _, nodeID := range responsible {
if !stores[nodeID].HasPost(hash) {
t.Errorf("after rebalance: node %s should have post for %s", nodeID, cid)
}
}
}

t.Logf("Rebalanced %d community-node assignments after adding node-3", rebalanced)
}

// Tests explicit community assignment overrides DHT ring routing.
func TestExplicitAssignmentOverridesRing(t *testing.T) {
d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 3})
stores := make(map[models.NodeID]*storage.ContentStore)

for i := 0; i < 5; i++ {
id := models.NodeID(fmt.Sprintf("node-%d", i))
d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[id] = cs
}

communityID := models.CommunityID("golang")

// Ring would normally assign to 3 nodes
ringNodes := d.LookupNodes(communityID)
if len(ringNodes) != 3 {
t.Fatalf("expected 3 ring nodes, got %d", len(ringNodes))
}

// Force a specific assignment
err := d.AssignCommunity(communityID, []models.NodeID{"node-0", "node-4"})
if err != nil {
t.Fatalf("assign failed: %v", err)
}

// Now only node-0 and node-4 should be responsible
assigned := d.LookupNodes(communityID)
if len(assigned) != 2 {
t.Errorf("expected 2 assigned nodes, got %d", len(assigned))
}

// Store content only on assigned nodes
post := &models.Post{
CommunityID: communityID,
AuthorID:    "user-1",
Title:       "Go concurrency",
Body:        "Let's discuss goroutines",
CreatedAt:   time.Now(),
}

for _, nodeID := range assigned {
stores[nodeID].StorePost(post)
}

// Verify: only assigned nodes have the content
for i := 0; i < 5; i++ {
id := models.NodeID(fmt.Sprintf("node-%d", i))
hasPosts := len(stores[id].GetCommunityPosts(communityID)) > 0
isAssigned := id == "node-0" || id == "node-4"

if isAssigned && !hasPosts {
t.Errorf("assigned node %s should have posts", id)
}
if !isAssigned && hasPosts {
t.Errorf("non-assigned node %s should not have posts", id)
}
}

// Unassign and verify fallback to ring
d.UnassignCommunity(communityID)
fallback := d.LookupNodes(communityID)
if len(fallback) != 3 {
t.Errorf("after unassign, should fall back to ring (3 nodes), got %d", len(fallback))
}
}

// Tests CRDT convergence: conflicting votes on different replicas converge.
func TestCRDTConvergence_ConflictingVotes(t *testing.T) {
d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 3})
stores := make(map[models.NodeID]*storage.ContentStore)
ids := []models.NodeID{"node-A", "node-B", "node-C"}

for _, id := range ids {
d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[id] = cs
}

communityID := models.CommunityID("golang")
responsible := d.LookupNodes(communityID)

// Create post on all replicas
post := &models.Post{
CommunityID: communityID,
AuthorID:    "user-1",
Title:       "Controversial take",
Body:        "Tabs vs Spaces",
CreatedAt:   time.Now(),
}

var hash models.ContentHash
for _, nodeID := range responsible {
h, _ := stores[nodeID].StorePost(post)
hash = h
}

// User-1 first upvotes on node A, then changes to downvote on node B
stores[responsible[0]].ApplyVote(models.Vote{
TargetHash: hash,
UserID:     "user-1",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, responsible[0])

time.Sleep(time.Millisecond) // ensure timestamp ordering

stores[responsible[1]].ApplyVote(models.Vote{
TargetHash: hash,
UserID:     "user-1",
Value:      models.Downvote,
Timestamp:  time.Now(),
}, responsible[1])

// User-2 upvotes on node C
stores[responsible[2]].ApplyVote(models.Vote{
TargetHash: hash,
UserID:     "user-2",
Value:      models.Upvote,
Timestamp:  time.Now(),
}, responsible[2])

// Merge all states
voteStates := make([]*crdt.VoteState, len(responsible))
for i, nodeID := range responsible {
voteStates[i] = stores[nodeID].GetVoteState(hash)
}

for _, nodeID := range responsible {
for _, vs := range voteStates {
stores[nodeID].MergeVoteState(vs)
}
}

// All nodes should have converged to the same score
scores := make(map[int64]int)
for _, nodeID := range responsible {
score, _ := stores[nodeID].GetVoteScore(hash)
scores[score]++
}

if len(scores) != 1 {
t.Errorf("CRDT convergence failed: different nodes report different scores: %v", scores)
}
}

// Tests the full lifecycle: create cluster → add content → node fails → rebalance → verify
func TestFullLifecycle_ClusterResilience(t *testing.T) {
d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 2})
stores := make(map[models.NodeID]*storage.ContentStore)

// Phase 1: Start cluster with 4 nodes
for i := 0; i < 4; i++ {
id := models.NodeID(fmt.Sprintf("node-%d", i))
d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[id] = cs
}

communityID := models.CommunityID("distributed-systems")

// Phase 2: Create content
post := &models.Post{
CommunityID: communityID,
AuthorID:    "prof-1",
Title:       "CAP Theorem Discussion",
Body:        "Let's discuss the trade-offs",
CreatedAt:   time.Now(),
}

responsible := d.LookupNodes(communityID)
var hash models.ContentHash
for _, nodeID := range responsible {
h, _ := stores[nodeID].StorePost(post)
hash = h
}

// Phase 3: Add votes
for i, nodeID := range responsible {
stores[nodeID].ApplyVote(models.Vote{
TargetHash: hash,
UserID:     models.UserID(fmt.Sprintf("user-%d", i)),
Value:      models.Upvote,
Timestamp:  time.Now(),
}, nodeID)
}

// Phase 4: Node 0 fails
failedNode := responsible[0]
survivors := make([]models.NodeID, 0)
for _, n := range responsible {
if n != failedNode {
survivors = append(survivors, n)
}
}

d.RemoveNode(failedNode)

// Phase 5: New responsible nodes
newResponsible := d.LookupNodes(communityID)

// Phase 6: Replicate from survivors to new nodes
for _, newNode := range newResponsible {
if !stores[newNode].HasPost(hash) {
// Find a survivor source
for _, src := range survivors {
if stores[src].HasPost(hash) {
p, _ := stores[src].GetPost(hash)
stores[newNode].StorePost(p)

// Also sync vote state
vs := stores[src].GetVoteState(hash)
stores[newNode].MergeVoteState(vs)
break
}
}
}
}

// Phase 7: Verify data survived
for _, nodeID := range newResponsible {
if !stores[nodeID].HasPost(hash) {
t.Errorf("after failover: node %s missing post", nodeID)
}

score, err := stores[nodeID].GetVoteScore(hash)
if err != nil {
t.Errorf("node %s: vote score error: %v", nodeID, err)
}
if score < 1 {
t.Errorf("node %s: expected score >= 1, got %d", nodeID, score)
}
}

// Phase 8: Add a new node (scaling up)
newID := models.NodeID("node-recovery")
d.AddNode(&models.NodeInfo{ID: newID, Address: "127.0.0.1", IsAlive: true})
cs, _ := storage.NewContentStore("")
stores[newID] = cs

// Check if the new node is now responsible
if d.IsResponsible(newID, communityID) {
// Replicate to it
for _, src := range newResponsible {
if stores[src].HasPost(hash) {
p, _ := stores[src].GetPost(hash)
stores[newID].StorePost(p)
vs := stores[src].GetVoteState(hash)
stores[newID].MergeVoteState(vs)
break
}
}

if !stores[newID].HasPost(hash) {
t.Errorf("recovery node should have the post after replication")
}
}
}
// =====================================
// CRDT Merge Property Tests
// =====================================

// Tests that CRDT merge is commutative: merge(A,B) == merge(B,A)
// and associative: merge(merge(A,B),C) == merge(A,merge(B,C))
// This is CRITICAL for deployment — nodes merge states in arbitrary order
// depending on network timing. If merge isn't commutative, nodes diverge.
func TestCRDTMerge_Commutativity(t *testing.T) {
	d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 3})
	ids := []models.NodeID{"node-A", "node-B", "node-C"}
	for _, id := range ids {
		d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
	}

	communityID := models.CommunityID("golang")
	responsible := d.LookupNodes(communityID)
	now := time.Now()

	post := &models.Post{
		CommunityID: communityID,
		AuthorID:    "user-1",
		Title:       "Commutativity test",
		Body:        "Testing merge order independence",
		CreatedAt:   now,
	}

	// Create 3 independent stores with the same post but different votes
	makeStore := func(nodeID models.NodeID, userID models.UserID, voteVal models.VoteType, ts time.Time) *storage.ContentStore {
		cs, _ := storage.NewContentStore("")
		hash, _ := cs.StorePost(post)
		cs.ApplyVote(models.Vote{TargetHash: hash, UserID: userID, Value: voteVal, Timestamp: ts}, nodeID)
		return cs
	}

	// Order 1: A→B, then result→C
	storeAB_1 := makeStore(responsible[0], "user-1", models.Upvote, now)
	storeAB_2 := makeStore(responsible[1], "user-2", models.Upvote, now.Add(time.Millisecond))
	storeAB_3 := makeStore(responsible[2], "user-3", models.Downvote, now.Add(2*time.Millisecond))

	hash := post.ComputeHash()

	vsA := storeAB_1.GetVoteState(hash)
	vsB := storeAB_2.GetVoteState(hash)
	vsC := storeAB_3.GetVoteState(hash)

	// merge(A,B) then merge(result,C)
	order1Store := makeStore(responsible[0], "user-1", models.Upvote, now)
	order1Store.MergeVoteState(vsB)
	order1Store.MergeVoteState(vsC)
	score1, _ := order1Store.GetVoteScore(hash)

	// merge(C,A) then merge(result,B) — reversed
	order2Store := makeStore(responsible[2], "user-3", models.Downvote, now.Add(2*time.Millisecond))
	order2Store.MergeVoteState(vsA)
	order2Store.MergeVoteState(vsB)
	score2, _ := order2Store.GetVoteScore(hash)

	// merge(B,C) then merge(result,A)
	order3Store := makeStore(responsible[1], "user-2", models.Upvote, now.Add(time.Millisecond))
	order3Store.MergeVoteState(vsC)
	order3Store.MergeVoteState(vsA)
	score3, _ := order3Store.GetVoteScore(hash)

	if score1 != score2 || score2 != score3 {
		t.Errorf("CRDT commutativity/associativity violation: order1=%d, order2=%d, order3=%d",
			score1, score2, score3)
	}
	t.Logf("All merge orders converged to score=%d", score1)
}

// Tests that merging the same state twice doesn't change the result.
// In deployment, gossip retransmissions cause duplicate merges constantly.
func TestCRDTMerge_Idempotency(t *testing.T) {
	store1, _ := storage.NewContentStore("")
	store2, _ := storage.NewContentStore("")

	post := &models.Post{
		CommunityID: "golang",
		AuthorID:    "user-1",
		Title:       "Idempotency test",
		Body:        "Testing double merge safety",
		CreatedAt:   time.Now(),
	}

	hash, _ := store1.StorePost(post)
	store2.StorePost(post)

	// Apply votes on store1
	store1.ApplyVote(models.Vote{TargetHash: hash, UserID: "user-1", Value: models.Upvote, Timestamp: time.Now()}, "node-1")
	store1.ApplyVote(models.Vote{TargetHash: hash, UserID: "user-2", Value: models.Downvote, Timestamp: time.Now()}, "node-1")

	vs := store1.GetVoteState(hash)

	// Merge once
	store2.MergeVoteState(vs)
	scoreAfterFirst, _ := store2.GetVoteScore(hash)

	// Merge again (simulating gossip retransmission)
	store2.MergeVoteState(vs)
	scoreAfterSecond, _ := store2.GetVoteScore(hash)

	// And again
	store2.MergeVoteState(vs)
	scoreAfterThird, _ := store2.GetVoteScore(hash)

	if scoreAfterFirst != scoreAfterSecond || scoreAfterSecond != scoreAfterThird {
		t.Errorf("idempotency violation: first=%d, second=%d, third=%d",
			scoreAfterFirst, scoreAfterSecond, scoreAfterThird)
	}
}

// Tests ALL 6 possible merge orderings of 3 nodes to guarantee convergence.
func TestCRDTMerge_AllOrderings(t *testing.T) {
	now := time.Now()
	post := &models.Post{
		CommunityID: "golang",
		AuthorID:    "user-1",
		Title:       "Permutation test",
		Body:        "Testing all merge orderings",
		CreatedAt:   now,
	}
	hash := post.ComputeHash()

	type voteSpec struct {
		nodeID models.NodeID
		userID models.UserID
		value  models.VoteType
		ts     time.Time
	}

	specs := []voteSpec{
		{"node-A", "user-1", models.Upvote, now},
		{"node-B", "user-2", models.Upvote, now.Add(time.Millisecond)},
		{"node-C", "user-3", models.Downvote, now.Add(2 * time.Millisecond)},
	}

	// Build independent vote states
	makeVS := func(spec voteSpec) *storage.ContentStore {
		cs, _ := storage.NewContentStore("")
		cs.StorePost(post)
		cs.ApplyVote(models.Vote{TargetHash: hash, UserID: spec.userID, Value: spec.value, Timestamp: spec.ts}, spec.nodeID)
		return cs
	}

	// All 6 permutations of [0,1,2]
	perms := [][3]int{
		{0, 1, 2}, {0, 2, 1}, {1, 0, 2},
		{1, 2, 0}, {2, 0, 1}, {2, 1, 0},
	}

	var scores []int64

	for _, perm := range perms {
		// Start from first in perm order, merge others
		srcStores := [3]*storage.ContentStore{
			makeVS(specs[0]), makeVS(specs[1]), makeVS(specs[2]),
		}

		// Get vote states BEFORE merging
		voteStates := [3]*crdt.VoteState{}
		for i := 0; i < 3; i++ {
			voteStates[i] = srcStores[i].GetVoteState(hash)
		}

		// Target store starts with first permutation element
		target, _ := storage.NewContentStore("")
		target.StorePost(post)
		target.ApplyVote(models.Vote{
			TargetHash: hash,
			UserID:     specs[perm[0]].userID,
			Value:      specs[perm[0]].value,
			Timestamp:  specs[perm[0]].ts,
		}, specs[perm[0]].nodeID)

		// Merge remaining in perm order
		target.MergeVoteState(voteStates[perm[1]])
		target.MergeVoteState(voteStates[perm[2]])

		score, _ := target.GetVoteScore(hash)
		scores = append(scores, score)
	}

	// All 6 orderings must produce the same score
	for i := 1; i < len(scores); i++ {
		if scores[i] != scores[0] {
			t.Errorf("merge ordering %d produced score %d, but ordering 0 produced %d (divergence!)",
				i, scores[i], scores[0])
		}
	}
	t.Logf("All 6 merge orderings converged to score=%d", scores[0])
}

// Tests DHT + Gossip network together — the actual deployment pattern.
// Content is broadcast via gossip, DHT determines which nodes are responsible.
func TestGossipDHT_Integration(t *testing.T) {
	// Set up 3 gossip nodes
	store1, _ := storage.NewContentStore("")
	store2, _ := storage.NewContentStore("")
	store3, _ := storage.NewContentStore("")

	node1, err := network.NewGossipNode(network.GossipConfig{
		NodeID:   "gossip-dht-1",
		BindAddr: "127.0.0.1",
		BindPort: 18101,
	}, store1)
	if err != nil {
		t.Fatalf("failed to create gossip node 1: %v", err)
	}
	defer node1.Shutdown()

	node2, err := network.NewGossipNode(network.GossipConfig{
		NodeID:   "gossip-dht-2",
		BindAddr: "127.0.0.1",
		BindPort: 18102,
	}, store2)
	if err != nil {
		t.Fatalf("failed to create gossip node 2: %v", err)
	}
	defer node2.Shutdown()

	node3, err := network.NewGossipNode(network.GossipConfig{
		NodeID:   "gossip-dht-3",
		BindAddr: "127.0.0.1",
		BindPort: 18103,
	}, store3)
	if err != nil {
		t.Fatalf("failed to create gossip node 3: %v", err)
	}
	defer node3.Shutdown()

	// Form gossip cluster
	_, err = node2.Join([]string{"127.0.0.1:18101"})
	if err != nil {
		t.Fatalf("node2 join failed: %v", err)
	}
	_, err = node3.Join([]string{"127.0.0.1:18101"})
	if err != nil {
		t.Fatalf("node3 join failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if node1.NumMembers() != 3 {
		t.Fatalf("expected 3 gossip members, got %d", node1.NumMembers())
	}

	// Set up DHT with the same nodes
	d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 2})
	d.AddNode(&models.NodeInfo{ID: "gossip-dht-1", Address: "127.0.0.1:18101", IsAlive: true})
	d.AddNode(&models.NodeInfo{ID: "gossip-dht-2", Address: "127.0.0.1:18102", IsAlive: true})
	d.AddNode(&models.NodeInfo{ID: "gossip-dht-3", Address: "127.0.0.1:18103", IsAlive: true})

	stores := map[models.NodeID]*storage.ContentStore{
		"gossip-dht-1": store1,
		"gossip-dht-2": store2,
		"gossip-dht-3": store3,
	}
	gossipNodes := map[models.NodeID]*network.GossipNode{
		"gossip-dht-1": node1,
		"gossip-dht-2": node2,
		"gossip-dht-3": node3,
	}

	// Create a post and broadcast it via gossip
	communityID := models.CommunityID("golang")
	responsible := d.LookupNodes(communityID)

	post := &models.Post{
		CommunityID: communityID,
		AuthorID:    "user-1",
		Title:       "Gossip+DHT Integration",
		Body:        "This post travels through gossip to all nodes",
		CreatedAt:   time.Now(),
	}

	// Store on primary node and broadcast
	primaryNode := responsible[0]
	hash, _ := stores[primaryNode].StorePost(post)
	err = gossipNodes[primaryNode].BroadcastPost(post)
	if err != nil {
		t.Fatalf("broadcast failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Gossip delivers to ALL nodes. Verify all received it.
	for nodeID, store := range stores {
		if !store.HasPost(hash) {
			t.Errorf("node %s did not receive post via gossip", nodeID)
		}
	}

	// Vote on different nodes
	stores[responsible[0]].ApplyVote(models.Vote{
		TargetHash: hash, UserID: "voter-1", Value: models.Upvote, Timestamp: time.Now(),
	}, responsible[0])

	if len(responsible) > 1 {
		stores[responsible[1]].ApplyVote(models.Vote{
			TargetHash: hash, UserID: "voter-2", Value: models.Upvote, Timestamp: time.Now(),
		}, responsible[1])
	}

	// Sync vote states via gossip
	for _, nodeID := range responsible {
		vs := stores[nodeID].GetVoteState(hash)
		if vs != nil {
			gossipNodes[nodeID].BroadcastVoteState(vs)
		}
	}

	time.Sleep(300 * time.Millisecond)

	// After gossip CRDT sync, responsible nodes should have converged scores
	for _, nodeID := range responsible {
		score, err := stores[nodeID].GetVoteScore(hash)
		if err != nil {
			t.Errorf("node %s: vote score error: %v", nodeID, err)
			continue
		}
		if score < 1 {
			t.Errorf("node %s: expected score >= 1 after gossip sync, got %d", nodeID, score)
		}
	}
}

// Tests that DHT operations remain consistent when both responsible nodes fail.
// The system must gracefully reassign without data loss (from surviving replicas).
func TestDHTCascadingFailures(t *testing.T) {
	d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 50, ReplicationFactor: 2})
	stores := make(map[models.NodeID]*storage.ContentStore)

	for i := 0; i < 6; i++ {
		id := models.NodeID(fmt.Sprintf("node-%d", i))
		d.AddNode(&models.NodeInfo{ID: id, Address: "127.0.0.1", IsAlive: true})
		cs, _ := storage.NewContentStore("")
		stores[id] = cs
	}

	communities := []models.CommunityID{"golang", "rust", "python", "java"}
	postHashes := make(map[models.CommunityID]models.ContentHash)

	// Store content on responsible nodes
	for _, cid := range communities {
		responsible := d.LookupNodes(cid)
		post := &models.Post{
			CommunityID: cid,
			AuthorID:    "user-1",
			Title:       fmt.Sprintf("Post in %s", cid),
			Body:        "Content",
			CreatedAt:   time.Now(),
		}
		for _, nodeID := range responsible {
			h, _ := stores[nodeID].StorePost(post)
			postHashes[cid] = h
		}
	}

	// Kill 3 out of 6 nodes (potentially both replicas for some communities)
	d.RemoveNode("node-0")
	d.RemoveNode("node-2")
	d.RemoveNode("node-4")

	// All communities should still have responsible nodes
	for _, cid := range communities {
		responsible := d.LookupNodes(cid)
		if len(responsible) == 0 {
			t.Fatalf("community %s has no responsible nodes after failures!", cid)
		}
		if len(responsible) != 2 {
			t.Errorf("community %s: expected 2 responsible nodes (from 3 survivors), got %d", cid, len(responsible))
		}

		// Check if at least one surviving responsible node has the data
		hash := postHashes[cid]
		foundData := false
		for _, nodeID := range responsible {
			if stores[nodeID].HasPost(hash) {
				foundData = true
				break
			}
		}

		if !foundData {
			// Data might be on a killed node — need replication from any survivor
			// This is expected when both original replicas are killed
			for _, nodeID := range responsible {
				// Check all surviving stores for this data
				for survivorID, store := range stores {
					if survivorID == "node-0" || survivorID == "node-2" || survivorID == "node-4" {
						continue // skip dead nodes
					}
					if store.HasPost(hash) {
						p, _ := store.GetPost(hash)
						stores[nodeID].StorePost(p)
						foundData = true
						break
					}
				}
			}
		}

		// Verify data can be recovered
		if !foundData {
			t.Logf("community %s: data was on killed nodes, no surviving replica — data loss expected with rf=2 and 50%% node failure", cid)
		}
	}
}

// Tests that DHT with different node ID formats (real-world diversity) still distributes evenly.
func TestDHT_RealWorldNodeIDs(t *testing.T) {
	d := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 150, ReplicationFactor: 3})

	// Real-world style node IDs
	realIDs := []string{
		"prod-us-east-1-server-001",
		"prod-us-west-2-server-042",
		"prod-eu-central-1-server-007",
		"staging-us-east-1-server-001",
		"prod-ap-southeast-1-server-013",
	}

	for _, id := range realIDs {
		d.AddNode(&models.NodeInfo{
			ID:      models.NodeID(id),
			Address: fmt.Sprintf("10.0.%d.1:7946", len(id)),
			IsAlive: true,
		})
	}

	communities := make([]models.CommunityID, 200)
	for i := 0; i < 200; i++ {
		communities[i] = models.CommunityID(fmt.Sprintf("r/%d", i))
	}

	dist := d.GetDistribution(communities)

	// Verify reasonable distribution (no node has 0 or > 50%)
	for _, id := range realIDs {
		count := dist[models.NodeID(id)]
		if count == 0 {
			t.Errorf("node %s has 0 communities — hash distribution failure", id)
		}
		if float64(count)/200 > 0.5 {
			t.Errorf("node %s has %d/200 communities (>50%%) — severe imbalance", id, count)
		}
	}
}