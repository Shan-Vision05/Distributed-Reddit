package dht

import (
"crypto/sha256"
"encoding/binary"
"fmt"
"sort"
"sync"

"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
)

const (
// DefaultVirtualNodes is the number of virtual nodes per physical node
// on the consistent hash ring. More virtual nodes = better distribution.
DefaultVirtualNodes = 150

// DefaultReplicationFactor is how many nodes should store each community.
DefaultReplicationFactor = 3
)

// CommunityDHT maps communities to responsible nodes using consistent hashing.
// It uses a hash ring with virtual nodes for even distribution and supports
// configurable replication factors.
type CommunityDHT struct {
mu sync.RWMutex

// Hash ring: sorted list of hashes
ring []uint64
// Map from hash position to node ID
ringMap map[uint64]models.NodeID
// Map from node ID to its info
nodes map[models.NodeID]*models.NodeInfo
// Community-to-nodes explicit assignment cache (overrides ring lookup)
assignments map[models.CommunityID][]models.NodeID

virtualNodes      int
replicationFactor int
}

// DHTConfig holds DHT configuration.
type DHTConfig struct {
VirtualNodes      int
ReplicationFactor int
}

// NewCommunityDHT creates a new DHT with the given configuration.
func NewCommunityDHT(cfg DHTConfig) *CommunityDHT {
vn := cfg.VirtualNodes
if vn <= 0 {
vn = DefaultVirtualNodes
}
rf := cfg.ReplicationFactor
if rf <= 0 {
rf = DefaultReplicationFactor
}

return &CommunityDHT{
ring:              make([]uint64, 0),
ringMap:           make(map[uint64]models.NodeID),
nodes:             make(map[models.NodeID]*models.NodeInfo),
assignments:       make(map[models.CommunityID][]models.NodeID),
virtualNodes:      vn,
replicationFactor: rf,
}
}

// AddNode adds a node to the hash ring.
func (d *CommunityDHT) AddNode(info *models.NodeInfo) {
d.mu.Lock()
defer d.mu.Unlock()

// Don't add duplicates
if _, exists := d.nodes[info.ID]; exists {
return
}

d.nodes[info.ID] = info

// Add virtual nodes to the ring
for i := 0; i < d.virtualNodes; i++ {
hash := d.hashKey(fmt.Sprintf("%s-vn-%d", info.ID, i))
d.ring = append(d.ring, hash)
d.ringMap[hash] = info.ID
}

// Sort the ring
sort.Slice(d.ring, func(i, j int) bool {
return d.ring[i] < d.ring[j]
})
}

// RemoveNode removes a node from the hash ring.
func (d *CommunityDHT) RemoveNode(nodeID models.NodeID) {
d.mu.Lock()
defer d.mu.Unlock()

if _, exists := d.nodes[nodeID]; !exists {
return
}

delete(d.nodes, nodeID)

// Remove all virtual nodes for this node
newRing := make([]uint64, 0, len(d.ring))
for _, hash := range d.ring {
if d.ringMap[hash] != nodeID {
newRing = append(newRing, hash)
} else {
delete(d.ringMap, hash)
}
}
d.ring = newRing

// Clean up any assignments that reference this node
for communityID, nodes := range d.assignments {
filtered := make([]models.NodeID, 0, len(nodes))
for _, n := range nodes {
if n != nodeID {
filtered = append(filtered, n)
}
}
if len(filtered) == 0 {
delete(d.assignments, communityID)
} else {
d.assignments[communityID] = filtered
}
}
}

// GetNodes returns the nodes currently in the ring.
func (d *CommunityDHT) GetNodes() []*models.NodeInfo {
d.mu.RLock()
defer d.mu.RUnlock()

result := make([]*models.NodeInfo, 0, len(d.nodes))
for _, info := range d.nodes {
result = append(result, info)
}
return result
}

// GetNode returns info for a specific node.
func (d *CommunityDHT) GetNode(nodeID models.NodeID) (*models.NodeInfo, bool) {
d.mu.RLock()
defer d.mu.RUnlock()
info, ok := d.nodes[nodeID]
return info, ok
}

// NodeCount returns the number of physical nodes in the ring.
func (d *CommunityDHT) NodeCount() int {
d.mu.RLock()
defer d.mu.RUnlock()
return len(d.nodes)
}

// RingSize returns the total number of entries (virtual nodes) in the ring.
func (d *CommunityDHT) RingSize() int {
d.mu.RLock()
defer d.mu.RUnlock()
return len(d.ring)
}

// LookupNodes finds the N responsible nodes for a community using the hash ring.
// Returns up to replicationFactor distinct physical nodes, walking clockwise
// from the community's hash position.
func (d *CommunityDHT) LookupNodes(communityID models.CommunityID) []models.NodeID {
d.mu.RLock()
defer d.mu.RUnlock()

// Check explicit assignment first
if assigned, ok := d.assignments[communityID]; ok && len(assigned) > 0 {
return append([]models.NodeID{}, assigned...)
}

return d.lookupFromRing(communityID)
}

// lookupFromRing walks the hash ring clockwise to find responsible nodes.
// Must be called with at least d.mu.RLock held.
func (d *CommunityDHT) lookupFromRing(communityID models.CommunityID) []models.NodeID {
if len(d.ring) == 0 {
return nil
}

hash := d.hashKey(string(communityID))

// Binary search for the first position >= hash (clockwise walk)
idx := sort.Search(len(d.ring), func(i int) bool {
return d.ring[i] >= hash
})

// Collect distinct physical nodes
result := make([]models.NodeID, 0, d.replicationFactor)
seen := make(map[models.NodeID]bool)

for i := 0; i < len(d.ring) && len(result) < d.replicationFactor; i++ {
pos := (idx + i) % len(d.ring)
nodeID := d.ringMap[d.ring[pos]]

if !seen[nodeID] {
seen[nodeID] = true
result = append(result, nodeID)
}
}

return result
}

// AssignCommunity explicitly assigns a community to specific nodes.
// This overrides the hash ring lookup for this community.
func (d *CommunityDHT) AssignCommunity(communityID models.CommunityID, nodes []models.NodeID) error {
d.mu.Lock()
defer d.mu.Unlock()

// Verify all nodes exist
for _, nodeID := range nodes {
if _, exists := d.nodes[nodeID]; !exists {
return fmt.Errorf("node %s not found in DHT", nodeID)
}
}

d.assignments[communityID] = append([]models.NodeID{}, nodes...)
return nil
}

// UnassignCommunity removes an explicit assignment, reverting to ring lookup.
func (d *CommunityDHT) UnassignCommunity(communityID models.CommunityID) {
d.mu.Lock()
defer d.mu.Unlock()
delete(d.assignments, communityID)
}

// GetAssignment returns the explicit assignment for a community, if any.
func (d *CommunityDHT) GetAssignment(communityID models.CommunityID) ([]models.NodeID, bool) {
d.mu.RLock()
defer d.mu.RUnlock()
nodes, ok := d.assignments[communityID]
if !ok {
return nil, false
}
return append([]models.NodeID{}, nodes...), true
}

// IsResponsible checks if a specific node is responsible for a community.
func (d *CommunityDHT) IsResponsible(nodeID models.NodeID, communityID models.CommunityID) bool {
nodes := d.LookupNodes(communityID)
for _, n := range nodes {
if n == nodeID {
return true
}
}
return false
}

// GetPrimaryNode returns the primary (first) responsible node for a community.
func (d *CommunityDHT) GetPrimaryNode(communityID models.CommunityID) (models.NodeID, bool) {
nodes := d.LookupNodes(communityID)
if len(nodes) == 0 {
return "", false
}
return nodes[0], true
}

// GetCommunitiesForNode returns all communities a node is responsible for,
// based on current ring state and explicit assignments.
func (d *CommunityDHT) GetCommunitiesForNode(nodeID models.NodeID, allCommunities []models.CommunityID) []models.CommunityID {
result := make([]models.CommunityID, 0)
for _, communityID := range allCommunities {
if d.IsResponsible(nodeID, communityID) {
result = append(result, communityID)
}
}
return result
}

// ReplicationFactor returns the configured replication factor.
func (d *CommunityDHT) ReplicationFactor() int {
return d.replicationFactor
}

// GetDistribution returns a map of node -> number of communities they're primary for.
// Useful for checking load balance across the ring.
func (d *CommunityDHT) GetDistribution(communities []models.CommunityID) map[models.NodeID]int {
dist := make(map[models.NodeID]int)
for _, communityID := range communities {
primary, ok := d.GetPrimaryNode(communityID)
if ok {
dist[primary]++
}
}
return dist
}

// hashKey produces a uint64 hash for a key string using SHA-256.
func (d *CommunityDHT) hashKey(key string) uint64 {
h := sha256.Sum256([]byte(key))
return binary.BigEndian.Uint64(h[:8])
}
