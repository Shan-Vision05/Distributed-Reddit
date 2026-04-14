package dht

import (
"fmt"
"math"
"testing"

"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
)

func makeNodeInfo(id string) *models.NodeInfo {
return &models.NodeInfo{
ID:      models.NodeID(id),
Address: fmt.Sprintf("127.0.0.1:%d", 9000+len(id)),
IsAlive: true,
}
}

func makeDHT(vnodes, replication int) *CommunityDHT {
return NewCommunityDHT(DHTConfig{
VirtualNodes:      vnodes,
ReplicationFactor: replication,
})
}

// ===== Construction & Defaults =====

func TestNewCommunityDHT_Defaults(t *testing.T) {
dht := NewCommunityDHT(DHTConfig{})
if dht.virtualNodes != DefaultVirtualNodes {
t.Errorf("expected %d virtual nodes, got %d", DefaultVirtualNodes, dht.virtualNodes)
}
if dht.replicationFactor != DefaultReplicationFactor {
t.Errorf("expected replication factor %d, got %d", DefaultReplicationFactor, dht.replicationFactor)
}
if dht.NodeCount() != 0 {
t.Errorf("expected 0 nodes, got %d", dht.NodeCount())
}
if dht.RingSize() != 0 {
t.Errorf("expected ring size 0, got %d", dht.RingSize())
}
}

func TestNewCommunityDHT_CustomConfig(t *testing.T) {
dht := makeDHT(200, 5)
if dht.virtualNodes != 200 {
t.Errorf("expected 200 virtual nodes, got %d", dht.virtualNodes)
}
if dht.ReplicationFactor() != 5 {
t.Errorf("expected replication factor 5, got %d", dht.ReplicationFactor())
}
}

func TestNewCommunityDHT_NegativeConfig(t *testing.T) {
dht := NewCommunityDHT(DHTConfig{VirtualNodes: -1, ReplicationFactor: -1})
if dht.virtualNodes != DefaultVirtualNodes {
t.Errorf("negative vnodes should default to %d, got %d", DefaultVirtualNodes, dht.virtualNodes)
}
if dht.replicationFactor != DefaultReplicationFactor {
t.Errorf("negative replication should default to %d, got %d", DefaultReplicationFactor, dht.replicationFactor)
}
}

// ===== AddNode =====

func TestAddNode_Single(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))

if dht.NodeCount() != 1 {
t.Errorf("expected 1 node, got %d", dht.NodeCount())
}
if dht.RingSize() != 10 {
t.Errorf("expected ring size 10, got %d", dht.RingSize())
}
}

func TestAddNode_Multiple(t *testing.T) {
dht := makeDHT(10, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

if dht.NodeCount() != 5 {
t.Errorf("expected 5 nodes, got %d", dht.NodeCount())
}
if dht.RingSize() != 50 {
t.Errorf("expected ring size 50, got %d", dht.RingSize())
}
}

func TestAddNode_Duplicate(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))
dht.AddNode(makeNodeInfo("node-1")) // duplicate

if dht.NodeCount() != 1 {
t.Errorf("duplicate add should be ignored, got %d nodes", dht.NodeCount())
}
if dht.RingSize() != 10 {
t.Errorf("duplicate add should not grow ring, got %d", dht.RingSize())
}
}

func TestAddNode_RingSorted(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 10; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

dht.mu.RLock()
defer dht.mu.RUnlock()
for i := 1; i < len(dht.ring); i++ {
if dht.ring[i] <= dht.ring[i-1] {
t.Fatalf("ring not sorted at position %d: %d <= %d", i, dht.ring[i], dht.ring[i-1])
}
}
}

// ===== RemoveNode =====

func TestRemoveNode_Basic(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))
dht.AddNode(makeNodeInfo("node-2"))

dht.RemoveNode("node-1")

if dht.NodeCount() != 1 {
t.Errorf("expected 1 node after removal, got %d", dht.NodeCount())
}
if dht.RingSize() != 10 {
t.Errorf("expected ring size 10, got %d", dht.RingSize())
}

_, ok := dht.GetNode("node-1")
if ok {
t.Error("removed node should not be found")
}
}

func TestRemoveNode_Nonexistent(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))
dht.RemoveNode("nonexistent") // should not panic

if dht.NodeCount() != 1 {
t.Errorf("expected 1 node, got %d", dht.NodeCount())
}
}

func TestRemoveNode_CleansAssignments(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))
dht.AddNode(makeNodeInfo("node-2"))

dht.AssignCommunity("golang", []models.NodeID{"node-1", "node-2"})
dht.RemoveNode("node-1")

assigned, ok := dht.GetAssignment("golang")
if !ok {
t.Fatal("assignment should still exist with remaining node")
}
if len(assigned) != 1 || assigned[0] != "node-2" {
t.Errorf("expected [node-2], got %v", assigned)
}
}

func TestRemoveNode_CleansAssignmentCompletely(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))

dht.AssignCommunity("golang", []models.NodeID{"node-1"})
dht.RemoveNode("node-1")

_, ok := dht.GetAssignment("golang")
if ok {
t.Error("assignment should be removed when all nodes are gone")
}
}

func TestRemoveNode_AllNodes(t *testing.T) {
dht := makeDHT(10, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}
for i := 0; i < 5; i++ {
dht.RemoveNode(models.NodeID(fmt.Sprintf("node-%d", i)))
}

if dht.NodeCount() != 0 {
t.Errorf("expected 0 nodes, got %d", dht.NodeCount())
}
if dht.RingSize() != 0 {
t.Errorf("expected 0 ring size, got %d", dht.RingSize())
}
}

// ===== GetNode / GetNodes =====

func TestGetNode(t *testing.T) {
dht := makeDHT(10, 3)
info := makeNodeInfo("node-1")
dht.AddNode(info)

got, ok := dht.GetNode("node-1")
if !ok {
t.Fatal("node should be found")
}
if got.ID != "node-1" {
t.Errorf("expected node-1, got %s", got.ID)
}
}

func TestGetNodes(t *testing.T) {
dht := makeDHT(10, 3)
for i := 0; i < 3; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

nodes := dht.GetNodes()
if len(nodes) != 3 {
t.Errorf("expected 3 nodes, got %d", len(nodes))
}
}

// ===== LookupNodes (Hash Ring) =====

func TestLookupNodes_EmptyRing(t *testing.T) {
dht := makeDHT(10, 3)
nodes := dht.LookupNodes("any-community")
if len(nodes) != 0 {
t.Errorf("empty ring should return 0 nodes, got %d", len(nodes))
}
}

func TestLookupNodes_SingleNode(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))

nodes := dht.LookupNodes("any-community")
if len(nodes) != 1 {
t.Fatalf("single node ring should return 1, got %d", len(nodes))
}
if nodes[0] != "node-1" {
t.Errorf("expected node-1, got %s", nodes[0])
}
}

func TestLookupNodes_RespectsReplicationFactor(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

nodes := dht.LookupNodes("golang")
if len(nodes) != 3 {
t.Errorf("expected 3 nodes (replication=3), got %d", len(nodes))
}

// All nodes should be distinct
seen := make(map[models.NodeID]bool)
for _, n := range nodes {
if seen[n] {
t.Errorf("duplicate node in result: %s", n)
}
seen[n] = true
}
}

func TestLookupNodes_LessNodesThanReplication(t *testing.T) {
dht := makeDHT(10, 5)
dht.AddNode(makeNodeInfo("node-1"))
dht.AddNode(makeNodeInfo("node-2"))

nodes := dht.LookupNodes("golang")
if len(nodes) != 2 {
t.Errorf("should return all 2 nodes when replication=5, got %d", len(nodes))
}
}

func TestLookupNodes_Deterministic(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

// Same community should always get the same nodes
result1 := dht.LookupNodes("golang")
result2 := dht.LookupNodes("golang")

if len(result1) != len(result2) {
t.Fatalf("lookup not deterministic: %d vs %d", len(result1), len(result2))
}
for i := range result1 {
if result1[i] != result2[i] {
t.Errorf("lookup not deterministic at pos %d: %s vs %s", i, result1[i], result2[i])
}
}
}

func TestLookupNodes_DifferentCommunities(t *testing.T) {
dht := makeDHT(150, 3)
for i := 0; i < 10; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

communities := []models.CommunityID{"golang", "rust", "python", "javascript", "haskell",
"ruby", "cpp", "java", "scala", "elixir"}

// Not all communities should map to the exact same set of nodes
results := make(map[string]bool)
for _, c := range communities {
nodes := dht.LookupNodes(c)
key := fmt.Sprintf("%v", nodes)
results[key] = true
}

if len(results) < 2 {
t.Error("10 communities on 10 nodes should have different node assignments")
}
}

// ===== Assignment (Explicit Override) =====

func TestAssignCommunity(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))
dht.AddNode(makeNodeInfo("node-2"))

err := dht.AssignCommunity("golang", []models.NodeID{"node-1", "node-2"})
if err != nil {
t.Fatalf("assignment failed: %v", err)
}

nodes := dht.LookupNodes("golang")
if len(nodes) != 2 {
t.Fatalf("expected 2 assigned nodes, got %d", len(nodes))
}
if nodes[0] != "node-1" || nodes[1] != "node-2" {
t.Errorf("expected [node-1, node-2], got %v", nodes)
}
}

func TestAssignCommunity_OverridesRing(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

// Lookup from ring first
ringNodes := dht.LookupNodes("golang")

// Override with explicit assignment
err := dht.AssignCommunity("golang", []models.NodeID{"node-4"})
if err != nil {
t.Fatalf("assignment failed: %v", err)
}

assigned := dht.LookupNodes("golang")
if len(assigned) != 1 || assigned[0] != "node-4" {
t.Errorf("expected [node-4], got %v", assigned)
}

// Verify it actually changed
if len(ringNodes) == 1 && ringNodes[0] == "node-4" {
// Unlikely but possible; skip further check
} else {
if fmt.Sprintf("%v", assigned) == fmt.Sprintf("%v", ringNodes) {
t.Error("assignment should override ring lookup")
}
}
}

func TestAssignCommunity_InvalidNode(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))

err := dht.AssignCommunity("golang", []models.NodeID{"node-1", "nonexistent"})
if err == nil {
t.Error("should fail when assigning to nonexistent node")
}
}

func TestUnassignCommunity(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

dht.AssignCommunity("golang", []models.NodeID{"node-0"})
dht.UnassignCommunity("golang")

// Should fall back to ring lookup
nodes := dht.LookupNodes("golang")
if len(nodes) != 3 {
t.Errorf("after unassign, should use ring (replication=3), got %d nodes", len(nodes))
}
}

func TestGetAssignment_Exists(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))

dht.AssignCommunity("golang", []models.NodeID{"node-1"})

assigned, ok := dht.GetAssignment("golang")
if !ok {
t.Error("expected assignment to exist")
}
if len(assigned) != 1 || assigned[0] != "node-1" {
t.Errorf("expected [node-1], got %v", assigned)
}
}

func TestGetAssignment_NotExists(t *testing.T) {
dht := makeDHT(10, 3)
_, ok := dht.GetAssignment("golang")
if ok {
t.Error("expected no assignment")
}
}

func TestGetAssignment_ReturnsCopy(t *testing.T) {
dht := makeDHT(10, 3)
dht.AddNode(makeNodeInfo("node-1"))
dht.AddNode(makeNodeInfo("node-2"))

dht.AssignCommunity("golang", []models.NodeID{"node-1"})

assigned, _ := dht.GetAssignment("golang")
assigned[0] = "node-2" // mutate returned slice

// Original should be unchanged
original, _ := dht.GetAssignment("golang")
if original[0] != "node-1" {
t.Error("mutation of returned slice should not affect DHT")
}
}

// ===== IsResponsible =====

func TestIsResponsible(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

nodes := dht.LookupNodes("golang")
for _, n := range nodes {
if !dht.IsResponsible(n, "golang") {
t.Errorf("node %s should be responsible for golang", n)
}
}
}

func TestIsResponsible_NotResponsible(t *testing.T) {
dht := makeDHT(10, 1)
dht.AddNode(makeNodeInfo("node-1"))
dht.AddNode(makeNodeInfo("node-2"))

primary, _ := dht.GetPrimaryNode("golang")
other := models.NodeID("node-1")
if primary == "node-1" {
other = "node-2"
}

if dht.IsResponsible(other, "golang") {
t.Errorf("node %s should NOT be responsible (replication=1)", other)
}
}

// ===== GetPrimaryNode =====

func TestGetPrimaryNode(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

primary, ok := dht.GetPrimaryNode("golang")
if !ok {
t.Fatal("should find primary node")
}

// Primary should be the first node from LookupNodes
nodes := dht.LookupNodes("golang")
if primary != nodes[0] {
t.Errorf("primary %s should be first lookup result %s", primary, nodes[0])
}
}

func TestGetPrimaryNode_EmptyRing(t *testing.T) {
dht := makeDHT(10, 3)
_, ok := dht.GetPrimaryNode("golang")
if ok {
t.Error("empty ring should return no primary")
}
}

// ===== GetCommunitiesForNode =====

func TestGetCommunitiesForNode(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 3; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

communities := []models.CommunityID{"golang", "rust", "python"}

// With replication=3 and 3 nodes, every node handles every community
for i := 0; i < 3; i++ {
nodeID := models.NodeID(fmt.Sprintf("node-%d", i))
responsible := dht.GetCommunitiesForNode(nodeID, communities)
if len(responsible) != 3 {
t.Errorf("node-%d: expected 3 communities (rf=3, 3 nodes), got %d", i, len(responsible))
}
}
}

// ===== GetDistribution =====

func TestGetDistribution(t *testing.T) {
dht := makeDHT(150, 1) // replication=1 to see clear primary distribution
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

communities := make([]models.CommunityID, 100)
for i := 0; i < 100; i++ {
communities[i] = models.CommunityID(fmt.Sprintf("community-%d", i))
}

dist := dht.GetDistribution(communities)

// All 5 nodes should have some communities
if len(dist) != 5 {
t.Errorf("expected all 5 nodes in distribution, got %d", len(dist))
}

// Each node should have at least some communities
for nodeID, count := range dist {
if count == 0 {
t.Errorf("node %s has 0 communities", nodeID)
}
}

// Total should be 100
total := 0
for _, count := range dist {
total += count
}
if total != 100 {
t.Errorf("total should be 100, got %d", total)
}
}

func TestGetDistribution_Balanced(t *testing.T) {
dht := makeDHT(150, 1)
nodeCount := 5
for i := 0; i < nodeCount; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

// Use enough communities to see statistical balance
communityCount := 1000
communities := make([]models.CommunityID, communityCount)
for i := 0; i < communityCount; i++ {
communities[i] = models.CommunityID(fmt.Sprintf("community-%d", i))
}

dist := dht.GetDistribution(communities)

// Each node should have roughly 1000/5 = 200 communities
// Allow 50% deviation for consistent hashing
expected := float64(communityCount) / float64(nodeCount)
for nodeID, count := range dist {
deviation := math.Abs(float64(count)-expected) / expected
if deviation > 0.5 {
t.Errorf("node %s has %d communities (expected ~%.0f, deviation %.1f%%)",
nodeID, count, expected, deviation*100)
}
}
}

// ===== Consistency Under Node Changes =====

func TestConsistency_AddNode(t *testing.T) {
dht := makeDHT(50, 1)
for i := 0; i < 3; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

// Record current assignments
communities := make([]models.CommunityID, 100)
for i := 0; i < 100; i++ {
communities[i] = models.CommunityID(fmt.Sprintf("community-%d", i))
}

before := make(map[models.CommunityID]models.NodeID)
for _, c := range communities {
primary, _ := dht.GetPrimaryNode(c)
before[c] = primary
}

// Add a new node
dht.AddNode(makeNodeInfo("node-3"))

// Count how many communities changed primary
changed := 0
for _, c := range communities {
primary, _ := dht.GetPrimaryNode(c)
if primary != before[c] {
changed++
}
}

// With consistent hashing, adding 1 node to 4 should move ~25% of keys
// Allow generous margin
changeRate := float64(changed) / float64(len(communities))
if changeRate > 0.5 {
t.Errorf("adding 1 node moved %.0f%% of communities (consistent hashing should limit this)", changeRate*100)
}
t.Logf("Adding node-3 relocated %d/%d communities (%.1f%%)", changed, len(communities), changeRate*100)
}

func TestConsistency_RemoveNode(t *testing.T) {
dht := makeDHT(50, 1)
for i := 0; i < 4; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

communities := make([]models.CommunityID, 100)
for i := 0; i < 100; i++ {
communities[i] = models.CommunityID(fmt.Sprintf("community-%d", i))
}

before := make(map[models.CommunityID]models.NodeID)
for _, c := range communities {
primary, _ := dht.GetPrimaryNode(c)
before[c] = primary
}

// Remove a node
dht.RemoveNode("node-2")

changed := 0
for _, c := range communities {
primary, _ := dht.GetPrimaryNode(c)
if primary != before[c] {
changed++
}
}

// Only communities on node-2 should have moved
changeRate := float64(changed) / float64(len(communities))
if changeRate > 0.5 {
t.Errorf("removing 1 node moved %.0f%% of communities", changeRate*100)
}
t.Logf("Removing node-2 relocated %d/%d communities (%.1f%%)", changed, len(communities), changeRate*100)
}

// ===== Concurrency Safety =====

func TestConcurrency(t *testing.T) {
dht := makeDHT(50, 3)
for i := 0; i < 5; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

done := make(chan bool, 100)

// Concurrent reads
for i := 0; i < 50; i++ {
go func(idx int) {
defer func() { done <- true }()
community := models.CommunityID(fmt.Sprintf("community-%d", idx))
_ = dht.LookupNodes(community)
_ = dht.IsResponsible("node-0", community)
_, _ = dht.GetPrimaryNode(community)
}(i)
}

// Concurrent writes
for i := 0; i < 25; i++ {
go func(idx int) {
defer func() { done <- true }()
nodeID := fmt.Sprintf("concurrent-node-%d", idx)
dht.AddNode(makeNodeInfo(nodeID))
_ = dht.NodeCount()
dht.RemoveNode(models.NodeID(nodeID))
}(i)
}

// Concurrent assignments
for i := 0; i < 25; i++ {
go func(idx int) {
defer func() { done <- true }()
cid := models.CommunityID(fmt.Sprintf("concurrent-comm-%d", idx))
_ = dht.AssignCommunity(cid, []models.NodeID{"node-0"})
_, _ = dht.GetAssignment(cid)
dht.UnassignCommunity(cid)
}(i)
}

for i := 0; i < 100; i++ {
<-done
}

// If we got here without panic/race, concurrency is safe
if dht.NodeCount() != 5 {
t.Errorf("after concurrent ops, expected 5 nodes, got %d", dht.NodeCount())
}
}

// ===== Edge Cases =====

func TestLookupNodes_ReplicationEqualToNodes(t *testing.T) {
dht := makeDHT(10, 3)
for i := 0; i < 3; i++ {
dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
}

nodes := dht.LookupNodes("golang")
if len(nodes) != 3 {
t.Errorf("expected exactly 3 nodes (rf=3, 3 nodes), got %d", len(nodes))
}

// All should be distinct
seen := make(map[models.NodeID]bool)
for _, n := range nodes {
if seen[n] {
t.Errorf("duplicate in result: %s", n)
}
seen[n] = true
}
}

func TestAddRemoveAdd(t *testing.T) {
dht := makeDHT(10, 1)
dht.AddNode(makeNodeInfo("node-1"))

before := dht.LookupNodes("golang")
dht.RemoveNode("node-1")

if dht.NodeCount() != 0 {
t.Fatal("node should be removed")
}

nodes := dht.LookupNodes("golang")
if len(nodes) != 0 {
t.Error("should return no nodes after removal")
}

dht.AddNode(makeNodeInfo("node-1"))
after := dht.LookupNodes("golang")

if len(before) != len(after) || before[0] != after[0] {
t.Errorf("re-adding same node should produce same mapping: %v vs %v", before, after)
}
}

func TestHashDeterminism(t *testing.T) {
dht := makeDHT(10, 3)
h1 := dht.hashKey("test-key")
h2 := dht.hashKey("test-key")
if h1 != h2 {
t.Error("hash function should be deterministic")
}

h3 := dht.hashKey("different-key")
if h1 == h3 {
t.Error("different keys should produce different hashes (collision is astronomically unlikely)")
}
}
// ===== Ring Wraparound =====
// In a real deployment, ~50% of community hashes land past the last ring position.
// sort.Search returns len(ring), and the modulo wraps to 0. This must work correctly.

func TestLookupNodes_RingWraparound(t *testing.T) {
	// Use 1 vnode per node so we can reason about ring positions
	dht := makeDHT(1, 2)
	dht.AddNode(makeNodeInfo("node-A"))
	dht.AddNode(makeNodeInfo("node-B"))

	// The ring has exactly 2 entries. Any community that hashes past the last
	// entry must wrap around to position 0. Try many communities to guarantee
	// at least some trigger the wraparound path.
	for i := 0; i < 50; i++ {
		community := models.CommunityID(fmt.Sprintf("wrap-test-%d", i))
		nodes := dht.LookupNodes(community)
		if len(nodes) != 2 {
			t.Fatalf("community %s: expected 2 nodes, got %d", community, len(nodes))
		}
		// Both nodes should be present (with rf=2 and 2 nodes)
		seen := map[models.NodeID]bool{nodes[0]: true, nodes[1]: true}
		if !seen["node-A"] || !seen["node-B"] {
			t.Errorf("community %s: expected both node-A and node-B, got %v", community, nodes)
		}
	}
}

func TestLookupNodes_RingWraparound_ExplicitVerification(t *testing.T) {
	// Verify the wraparound by checking the actual binary search index
	dht := makeDHT(1, 1)
	dht.AddNode(makeNodeInfo("node-X"))

	// With a single vnode, the ring has exactly 1 entry.
	// For a community whose hash > ring[0], sort.Search returns 1 (past end),
	// and (1 % 1) == 0 wraps back to the only entry.
	// All communities must resolve to node-X regardless.
	for i := 0; i < 100; i++ {
		community := models.CommunityID(fmt.Sprintf("c-%d", i))
		nodes := dht.LookupNodes(community)
		if len(nodes) != 1 || nodes[0] != "node-X" {
			t.Fatalf("community %s should map to node-X, got %v", community, nodes)
		}
	}
}

// ===== Cross-Instance Determinism =====
// In deployment, EVERY node runs its own independent DHT instance.
// Two instances with the same config and nodes MUST produce identical routing.

func TestCrossInstance_Determinism(t *testing.T) {
	// Create two independent DHT instances with the same config
	dht1 := makeDHT(150, 3)
	dht2 := makeDHT(150, 3)

	// Add the same nodes (in the same order)
	for i := 0; i < 5; i++ {
		info := makeNodeInfo(fmt.Sprintf("node-%d", i))
		dht1.AddNode(info)
		dht2.AddNode(info)
	}

	// Both must produce identical results for all communities
	for i := 0; i < 100; i++ {
		community := models.CommunityID(fmt.Sprintf("community-%d", i))

		nodes1 := dht1.LookupNodes(community)
		nodes2 := dht2.LookupNodes(community)

		if len(nodes1) != len(nodes2) {
			t.Fatalf("community %s: instance1 returned %d nodes, instance2 returned %d",
				community, len(nodes1), len(nodes2))
		}

		for j := range nodes1 {
			if nodes1[j] != nodes2[j] {
				t.Fatalf("community %s: divergence at position %d: %s vs %s",
					community, j, nodes1[j], nodes2[j])
			}
		}
	}
}

// ===== Node Add-Order Independence =====
// Nodes may join the cluster in any order. The final routing MUST be identical.

func TestAddNode_OrderIndependence(t *testing.T) {
	nodeNames := []string{"node-alpha", "node-beta", "node-gamma", "node-delta", "node-epsilon"}

	// Instance 1: add in forward order
	dht1 := makeDHT(50, 3)
	for _, name := range nodeNames {
		dht1.AddNode(makeNodeInfo(name))
	}

	// Instance 2: add in reverse order
	dht2 := makeDHT(50, 3)
	for i := len(nodeNames) - 1; i >= 0; i-- {
		dht2.AddNode(makeNodeInfo(nodeNames[i]))
	}

	// Instance 3: add in a scrambled order
	dht3 := makeDHT(50, 3)
	scrambled := []string{"node-gamma", "node-alpha", "node-epsilon", "node-delta", "node-beta"}
	for _, name := range scrambled {
		dht3.AddNode(makeNodeInfo(name))
	}

	// All three must produce identical routing
	for i := 0; i < 100; i++ {
		community := models.CommunityID(fmt.Sprintf("community-%d", i))
		r1 := dht1.LookupNodes(community)
		r2 := dht2.LookupNodes(community)
		r3 := dht3.LookupNodes(community)

		if len(r1) != len(r2) || len(r1) != len(r3) {
			t.Fatalf("community %s: different result lengths across instances", community)
		}

		for j := range r1 {
			if r1[j] != r2[j] || r1[j] != r3[j] {
				t.Fatalf("community %s pos %d: forward=%s reverse=%s scrambled=%s",
					community, j, r1[j], r2[j], r3[j])
			}
		}
	}
}

// ===== LookupNodes Returns Defensive Copy =====

func TestLookupNodes_ReturnsCopy_RingPath(t *testing.T) {
	dht := makeDHT(50, 3)
	for i := 0; i < 5; i++ {
		dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
	}

	result1 := dht.LookupNodes("golang")
	original := make([]models.NodeID, len(result1))
	copy(original, result1)

	// Mutate the returned slice
	result1[0] = "CORRUPTED"

	// Subsequent lookup must be unaffected
	result2 := dht.LookupNodes("golang")
	for i := range original {
		if result2[i] != original[i] {
			t.Errorf("mutation of returned slice affected DHT: pos %d is %s, expected %s",
				i, result2[i], original[i])
		}
	}
}

func TestLookupNodes_ReturnsCopy_AssignmentPath(t *testing.T) {
	dht := makeDHT(10, 3)
	dht.AddNode(makeNodeInfo("node-1"))
	dht.AddNode(makeNodeInfo("node-2"))

	dht.AssignCommunity("golang", []models.NodeID{"node-1", "node-2"})

	result := dht.LookupNodes("golang")
	result[0] = "CORRUPTED"

	result2 := dht.LookupNodes("golang")
	if result2[0] != "node-1" {
		t.Error("mutation of returned assignment slice corrupted DHT state")
	}
}

// ===== Empty Assignment Edge Case =====

func TestAssignCommunity_EmptyList(t *testing.T) {
	dht := makeDHT(50, 3)
	for i := 0; i < 3; i++ {
		dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
	}

	// Assigning an empty list should either error or fall through to ring
	err := dht.AssignCommunity("golang", []models.NodeID{})

	// Regardless of whether it errors, lookup should still work
	nodes := dht.LookupNodes("golang")
	if len(nodes) == 0 {
		t.Error("empty assignment should not break lookups — must fall back to ring")
	}

	// If no error, GetAssignment should return the empty list
	// but LookupNodes must still work (fall back to ring)
	if err == nil {
		assigned, ok := dht.GetAssignment("golang")
		if ok && len(assigned) == 0 {
			// This is OK — empty assignment, ring fallback works
			if len(nodes) != 3 {
				t.Errorf("expected 3 ring nodes as fallback, got %d", len(nodes))
			}
		}
	}
}

// ===== Reassign (Overwrite) =====

func TestAssignCommunity_Reassign(t *testing.T) {
	dht := makeDHT(10, 3)
	dht.AddNode(makeNodeInfo("node-1"))
	dht.AddNode(makeNodeInfo("node-2"))
	dht.AddNode(makeNodeInfo("node-3"))

	dht.AssignCommunity("golang", []models.NodeID{"node-1"})
	nodes1 := dht.LookupNodes("golang")
	if len(nodes1) != 1 || nodes1[0] != "node-1" {
		t.Fatalf("first assignment failed: %v", nodes1)
	}

	// Overwrite with different assignment
	dht.AssignCommunity("golang", []models.NodeID{"node-2", "node-3"})
	nodes2 := dht.LookupNodes("golang")
	if len(nodes2) != 2 {
		t.Fatalf("reassignment should have 2 nodes, got %d", len(nodes2))
	}
	if nodes2[0] != "node-2" || nodes2[1] != "node-3" {
		t.Errorf("expected [node-2, node-3], got %v", nodes2)
	}
}

// ===== Cascading Failures Beyond Replication Factor =====

func TestCascadingFailures(t *testing.T) {
	dht := makeDHT(50, 2)
	for i := 0; i < 5; i++ {
		dht.AddNode(makeNodeInfo(fmt.Sprintf("node-%d", i)))
	}

	community := models.CommunityID("golang")

	// Get original responsible nodes
	original := dht.LookupNodes(community)
	if len(original) != 2 {
		t.Fatalf("expected 2 responsible nodes, got %d", len(original))
	}

	// Kill BOTH responsible nodes (exceeds replication factor)
	dht.RemoveNode(original[0])
	dht.RemoveNode(original[1])

	// System should still work — the community gets remapped to remaining nodes
	remaining := dht.LookupNodes(community)
	if len(remaining) != 2 {
		t.Errorf("after losing both replicas, should remap to 2 surviving nodes, got %d", len(remaining))
	}

	// Remaining nodes should be from the survivors
	for _, n := range remaining {
		if n == original[0] || n == original[1] {
			t.Errorf("remapped to a dead node: %s", n)
		}
	}

	// Kill ALL but one
	for i := 0; i < 5; i++ {
		id := models.NodeID(fmt.Sprintf("node-%d", i))
		if id != remaining[0] {
			dht.RemoveNode(id)
		}
	}

	// Should still return the last surviving node
	last := dht.LookupNodes(community)
	if len(last) != 1 {
		t.Errorf("with 1 node left, should return 1, got %d", len(last))
	}
}