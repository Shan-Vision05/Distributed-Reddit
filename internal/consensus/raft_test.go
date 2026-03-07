package consensus

import (
"testing"
"time"

"github.com/hashicorp/raft"

"github.com/Shan-Vision05/DReddit/internal/models"
)

func createTestCluster(t *testing.T, n int) ([]*RaftNode, []raft.LoopbackTransport) {
t.Helper()

transports := make([]raft.LoopbackTransport, n)
addrs := make([]raft.ServerAddress, n)
ids := make([]string, n)

for i := 0; i < n; i++ {
ids[i] = string(rune('A' + i))
_, trans := raft.NewInmemTransport(raft.ServerAddress(ids[i]))
transports[i] = trans
addrs[i] = trans.LocalAddr()
}

// Connect all transports to each other
for i := 0; i < n; i++ {
for j := 0; j < n; j++ {
if i != j {
transports[i].Connect(addrs[j], transports[j])
}
}
}

nodes := make([]*RaftNode, n)
for i := 0; i < n; i++ {
node, err := NewInMemRaftNode(
models.NodeID(ids[i]),
"test-community",
transports[i],
)
if err != nil {
t.Fatalf("failed to create node %s: %v", ids[i], err)
}
nodes[i] = node
}

// Bootstrap the first node with the full cluster configuration
servers := make([]raft.Server, n)
for i := 0; i < n; i++ {
servers[i] = raft.Server{
ID:      raft.ServerID(ids[i]),
Address: addrs[i],
}
}
f := nodes[0].raft.BootstrapCluster(raft.Configuration{Servers: servers})
if err := f.Error(); err != nil {
t.Fatalf("bootstrap failed: %v", err)
}

return nodes, transports
}

func waitForLeader(t *testing.T, nodes []*RaftNode, timeout time.Duration) *RaftNode {
t.Helper()
deadline := time.After(timeout)
for {
select {
case <-deadline:
t.Fatal("timed out waiting for leader")
return nil
default:
for _, n := range nodes {
if n.IsLeader() {
return n
}
}
time.Sleep(10 * time.Millisecond)
}
}
}

func shutdownAll(t *testing.T, nodes []*RaftNode) {
t.Helper()
for _, n := range nodes {
if err := n.Shutdown(); err != nil {
t.Logf("shutdown error: %v", err)
}
}
}

// Tests

func TestFSMApply(t *testing.T) {
fsm := NewModerationFSM()

actions := fsm.GetLog()
if len(actions) != 0 {
t.Fatalf("expected empty log, got %d entries", len(actions))
}
}

func TestFSMSnapshot(t *testing.T) {
fsm := NewModerationFSM()
snap, err := fsm.Snapshot()
if err != nil {
t.Fatalf("Snapshot() error: %v", err)
}
if snap == nil {
t.Fatal("expected non-nil snapshot")
}
}

func TestSingleNodeCluster(t *testing.T) {
nodes, _ := createTestCluster(t, 1)
defer shutdownAll(t, nodes)

leader := waitForLeader(t, nodes, 3*time.Second)
if leader == nil {
t.Fatal("no leader elected")
}
if !leader.IsLeader() {
t.Error("single node should be leader")
}
}

func TestLeaderElection(t *testing.T) {
nodes, _ := createTestCluster(t, 3)
defer shutdownAll(t, nodes)

leader := waitForLeader(t, nodes, 3*time.Second)
if leader == nil {
t.Fatal("no leader elected")
}

leaderCount := 0
for _, n := range nodes {
if n.IsLeader() {
leaderCount++
}
}
if leaderCount != 1 {
t.Errorf("expected 1 leader, got %d", leaderCount)
}
}

func TestPropose(t *testing.T) {
nodes, _ := createTestCluster(t, 3)
defer shutdownAll(t, nodes)

leader := waitForLeader(t, nodes, 3*time.Second)

action := models.ModerationAction{
ID:          "mod-1",
CommunityID: "test-community",
ModeratorID: "moderator-1",
ActionType:  models.ModRemovePost,
TargetHash:  "abc123",
Reason:      "spam",
Timestamp:   time.Now(),
}

err := leader.Propose(action)
if err != nil {
t.Fatalf("Propose() error: %v", err)
}

log := leader.GetLog()
if len(log) != 1 {
t.Fatalf("expected 1 log entry, got %d", len(log))
}
if log[0].ID != "mod-1" {
t.Errorf("expected ID mod-1, got %s", log[0].ID)
}
if log[0].ActionType != models.ModRemovePost {
t.Errorf("expected REMOVE_POST, got %s", log[0].ActionType)
}
if log[0].LogIndex == 0 {
t.Error("LogIndex should be set by Raft")
}
}

func TestProposeMultiple(t *testing.T) {
nodes, _ := createTestCluster(t, 3)
defer shutdownAll(t, nodes)

leader := waitForLeader(t, nodes, 3*time.Second)

actions := []models.ModerationAction{
{ID: "mod-1", ActionType: models.ModRemovePost, TargetHash: "hash1", Timestamp: time.Now()},
{ID: "mod-2", ActionType: models.ModBanUser, TargetUser: "user1", Timestamp: time.Now()},
{ID: "mod-3", ActionType: models.ModRestorePost, TargetHash: "hash1", Timestamp: time.Now()},
}

for _, a := range actions {
if err := leader.Propose(a); err != nil {
t.Fatalf("Propose(%s) error: %v", a.ID, err)
}
}

log := leader.GetLog()
if len(log) != 3 {
t.Fatalf("expected 3 log entries, got %d", len(log))
}
for i, entry := range log {
if entry.ID != actions[i].ID {
t.Errorf("entry %d: expected ID %s, got %s", i, actions[i].ID, entry.ID)
}
}
}

func TestLogReplication(t *testing.T) {
nodes, _ := createTestCluster(t, 3)
defer shutdownAll(t, nodes)

leader := waitForLeader(t, nodes, 3*time.Second)

action := models.ModerationAction{
ID:         "mod-1",
ActionType: models.ModBanUser,
TargetUser: "baduser",
Reason:     "abuse",
Timestamp:  time.Now(),
}
if err := leader.Propose(action); err != nil {
t.Fatalf("Propose() error: %v", err)
}

// Wait for replication
time.Sleep(200 * time.Millisecond)

for i, n := range nodes {
log := n.GetLog()
if len(log) != 1 {
t.Errorf("node %d: expected 1 log entry, got %d", i, len(log))
continue
}
if log[0].ID != "mod-1" {
t.Errorf("node %d: expected ID mod-1, got %s", i, log[0].ID)
}
}
}

func TestNodeState(t *testing.T) {
nodes, _ := createTestCluster(t, 3)
defer shutdownAll(t, nodes)

waitForLeader(t, nodes, 3*time.Second)

for _, n := range nodes {
state := n.State()
if state != "Leader" && state != "Follower" {
t.Errorf("unexpected state: %s", state)
}
}
}

func TestNodeIdentity(t *testing.T) {
nodes, _ := createTestCluster(t, 3)
defer shutdownAll(t, nodes)

if nodes[0].NodeID() != "A" {
t.Errorf("expected node ID A, got %s", nodes[0].NodeID())
}
if nodes[0].CommunityID() != "test-community" {
t.Errorf("expected community test-community, got %s", nodes[0].CommunityID())
}
}

func TestDataDir(t *testing.T) {
dir := DataDir("/var/data", "gaming")
expected := "/var/data/raft/gaming"
if dir != expected {
t.Errorf("DataDir = %s, want %s", dir, expected)
}
}
