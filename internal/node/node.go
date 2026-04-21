package node

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/community"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/consensus"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/dht"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/network"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/storage"
)

type Node struct {
	NodeID models.NodeID
	DHT    *dht.CommunityDHT
	Gossip *network.GossipNode

	mu                sync.RWMutex
	dataDir           string                // root data directory for this node
	store             *storage.ContentStore // shared store for all community managers
	communities       map[models.CommunityID]*community.Manager
	raftNodes         map[models.CommunityID]*consensus.RaftNode
	raftAddrs         map[models.CommunityID]string // this node's Raft TCP bind address per community
	remoteCommunities map[models.CommunityID]bool   // communities announced by OTHER nodes
}

// NodeConfig holds optional configuration for NewNodeWithConfig.
// Zero-values mean "pick a sensible default".
type NodeConfig struct {
	GossipPort  int      // 0 = pick a random port in 10000-19999
	GossipPeers []string // seed addresses ("host:port") to join at startup
	DataDir     string   // root directory for Raft logs, snapshots, and content store; "" = cwd
}

// NewNode creates a node with a random gossip port and no pre-configured peers.
// Existing callers (tests, cmd/dreddit) continue to work unchanged.
func NewNode(nodeID models.NodeID, bindAddr string) (*Node, error) {
	return NewNodeWithConfig(nodeID, bindAddr, NodeConfig{})
}

// NewNodeWithConfig is the full constructor that accepts explicit gossip config.
func NewNodeWithConfig(nodeID models.NodeID, bindAddr string, cfg NodeConfig) (*Node, error) {
	// Set up data directory (per-node isolation).
	dataDir := cfg.DataDir
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create data dir: %v", err)
		}
	}

	dhtNode := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 150, ReplicationFactor: 3})
	dhtNode.AddNode(&models.NodeInfo{ID: nodeID, Address: bindAddr, IsAlive: true})

	// Content store uses a dedicated sub-directory so posts/comments/votes survive restarts.
	storeDir := filepath.Join(dataDir, "store")
	if dataDir == "" {
		storeDir = ""
	}
	store, err := storage.NewContentStore(storeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create content store: %v", err)
	}

	rand.Seed(time.Now().UnixNano())
	gossipPort := cfg.GossipPort
	if gossipPort == 0 {
		gossipPort = 10000 + rand.Intn(10000)
	}

	gossipNode, err := network.NewGossipNode(network.GossipConfig{
		NodeID:   nodeID,
		BindAddr: "127.0.0.1",
		BindPort: gossipPort,
	}, store)
	if err != nil {
		return nil, fmt.Errorf("failed to start gossip: %v", err)
	}

	n := &Node{
		NodeID:            nodeID,
		DHT:               dhtNode,
		Gossip:            gossipNode,
		dataDir:           dataDir,
		store:             store,
		communities:       make(map[models.CommunityID]*community.Manager),
		raftNodes:         make(map[models.CommunityID]*consensus.RaftNode),
		raftAddrs:         make(map[models.CommunityID]string),
		remoteCommunities: make(map[models.CommunityID]bool),
	}

	// Keep the DHT ring in sync with gossip membership changes.
	// When a new peer joins, re-broadcast all our community announcements so the
	// new peer learns which communities are already hosted and uses follower mode.
	gossipNode.SetOnPeerJoin(func(id models.NodeID, addr string) {
		dhtNode.AddNode(&models.NodeInfo{ID: id, Address: addr, IsAlive: true})
		n.mu.RLock()
		addrs := make(map[models.CommunityID]string, len(n.raftAddrs))
		for cid, ra := range n.raftAddrs {
			addrs[cid] = ra
		}
		n.mu.RUnlock()
		for cid, ra := range addrs {
			_ = gossipNode.BroadcastCommunityAnnounce(string(cid), string(nodeID), ra)
		}
	})
	gossipNode.SetOnPeerLeave(func(id models.NodeID) {
		dhtNode.RemoveNode(id)
	})

	// When a peer announces it has joined a community:
	//   1. Update DHT assignments so every node knows who hosts what.
	//   2. Record that this community is hosted on a REMOTE node (used by
	//      handleJoinCommunity to decide bootstrap vs follower mode).
	//   3. If we are the Raft leader, add the peer as a new voter.
	gossipNode.SetOnCommunityAnnounce(func(communityID, peerNodeID, raftAddr string) {
		cid := models.CommunityID(communityID)
		dhtNode.AssignCommunity(cid, []models.NodeID{models.NodeID(peerNodeID)})

		n.mu.Lock()
		// Only mark as remote when a DIFFERENT node announces it.
		if models.NodeID(peerNodeID) != nodeID {
			n.remoteCommunities[cid] = true
		}
		raftNode := n.raftNodes[cid]
		n.mu.Unlock()

		if raftNode != nil && raftNode.IsLeader() {
			_ = raftNode.AddVoter(models.NodeID(peerNodeID), raftAddr)
		}
	})

	// Join existing gossip cluster if seed peers were provided.
	if len(cfg.GossipPeers) > 0 {
		if _, err := gossipNode.Join(cfg.GossipPeers); err != nil {
			return nil, fmt.Errorf("failed to join gossip cluster: %v", err)
		}
	}

	// Auto-rejoin communities whose Raft data directories still exist on disk.
	// Only run this when a dataDir was explicitly configured; scanning CWD would
	// resurrect stale Raft databases from previous runs or other nodes.
	if dataDir != "" {
		files, _ := os.ReadDir(dataDir)
		for _, f := range files {
			if !f.IsDir() {
				continue
			}
			if strings.HasPrefix(f.Name(), "raft_") {
				commID := strings.TrimPrefix(f.Name(), "raft_")
				if commID != "" {
					_ = n.JoinCommunity(models.CommunityID(commID))
				}
			}
		}
	}

	return n, nil
}

// raftDataDir returns the Raft data directory for the given community.
func (n *Node) raftDataDir(communityID models.CommunityID) string {
	name := fmt.Sprintf("raft_%s", communityID)
	if n.dataDir != "" {
		return filepath.Join(n.dataDir, name)
	}
	return name
}

func (n *Node) raftAddrFile(communityID models.CommunityID) string {
	return filepath.Join(n.raftDataDir(communityID), "raft_addr")
}

func (n *Node) loadOrCreateRaftAddr(communityID models.CommunityID) (string, error) {
	if addr, ok := n.raftAddrs[communityID]; ok && addr != "" {
		return addr, nil
	}

	if n.dataDir != "" {
		if data, err := os.ReadFile(n.raftAddrFile(communityID)); err == nil {
			addr := strings.TrimSpace(string(data))
			if addr != "" {
				return addr, nil
			}
		}
	}

	addr := fmt.Sprintf("127.0.0.1:%d", 20000+rand.Intn(10000))
	if n.dataDir == "" {
		return addr, nil
	}

	if err := os.MkdirAll(n.raftDataDir(communityID), 0755); err != nil {
		return "", fmt.Errorf("create raft data dir: %w", err)
	}
	if err := os.WriteFile(n.raftAddrFile(communityID), []byte(addr+"\n"), 0644); err != nil {
		return "", fmt.Errorf("persist raft addr: %w", err)
	}

	return addr, nil
}

func (n *Node) JoinCommunity(communityID models.CommunityID) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, exists := n.communities[communityID]; exists {
		return fmt.Errorf("already a member of community %s", communityID)
	}

	raftAddr, err := n.loadOrCreateRaftAddr(communityID)
	if err != nil {
		return fmt.Errorf("resolve raft addr: %v", err)
	}
	raftCfg := consensus.RaftConfig{
		NodeID:      n.NodeID,
		CommunityID: communityID,
		BindAddr:    raftAddr,
		Bootstrap:   true,
		DataDir:     n.raftDataDir(communityID),
	}

	raftNode, err := consensus.NewRaftNode(raftCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize raft for community: %v", err)
	}

	manager := community.NewManager(communityID, n.NodeID, n.store, n.Gossip, raftNode)
	n.communities[communityID] = manager
	n.raftNodes[communityID] = raftNode
	n.raftAddrs[communityID] = raftAddr

	// Broadcast community membership so peers can update their DHT assignment tables
	// and the leader can add this node as a Raft voter.
	_ = n.Gossip.BroadcastCommunityAnnounce(string(communityID), string(n.NodeID), raftAddr)

	return nil
}

func (n *Node) GetCommunity(communityID models.CommunityID) (*community.Manager, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	manager, exists := n.communities[communityID]
	if !exists {
		return nil, fmt.Errorf("not a member of community %s", communityID)
	}
	return manager, nil
}

func (n *Node) GetJoinedCommunities() []models.CommunityID {
	n.mu.RLock()
	defer n.mu.RUnlock()

	var list []models.CommunityID
	for cid := range n.communities {
		list = append(list, cid)
	}
	return list
}

// JoinCommunityAsFollower joins the node to an existing multi-node community as a
// Raft follower. The node does NOT bootstrap a new cluster — it waits for the leader
// to add it via AddVoter. The caller must ensure the leader subsequently calls
// AddRaftPeer(communityID, thisNodeID, thisRaftAddr) to complete the join.
func (n *Node) JoinCommunityAsFollower(communityID models.CommunityID) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, exists := n.communities[communityID]; exists {
		return fmt.Errorf("already a member of community %s", communityID)
	}

	raftAddr, err := n.loadOrCreateRaftAddr(communityID)
	if err != nil {
		return fmt.Errorf("resolve raft addr: %v", err)
	}
	raftCfg := consensus.RaftConfig{
		NodeID:      n.NodeID,
		CommunityID: communityID,
		BindAddr:    raftAddr,
		Bootstrap:   false, // follower: do NOT bootstrap a new cluster
		DataDir:     n.raftDataDir(communityID),
	}

	raftNode, err := consensus.NewRaftNode(raftCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize raft follower: %v", err)
	}

	manager := community.NewManager(communityID, n.NodeID, n.store, n.Gossip, raftNode)
	n.communities[communityID] = manager
	n.raftNodes[communityID] = raftNode
	n.raftAddrs[communityID] = raftAddr

	// Broadcast so the leader can discover us and call AddVoter.
	_ = n.Gossip.BroadcastCommunityAnnounce(string(communityID), string(n.NodeID), raftAddr)

	return nil
}

// AddRaftPeer adds a remote node as a Raft voter for the given community.
// This node must currently be the Raft leader for the community.
func (n *Node) AddRaftPeer(communityID models.CommunityID, peerNodeID models.NodeID, peerRaftAddr string) error {
	n.mu.RLock()
	raftNode, ok := n.raftNodes[communityID]
	n.mu.RUnlock()
	if !ok {
		return fmt.Errorf("not a member of community %s", communityID)
	}
	return raftNode.AddVoter(peerNodeID, peerRaftAddr)
}

// GetRaftAddr returns the Raft TCP bind address this node uses for the given community.
func (n *Node) GetRaftAddr(communityID models.CommunityID) (string, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	addr, ok := n.raftAddrs[communityID]
	if !ok {
		return "", fmt.Errorf("not a member of community %s", communityID)
	}
	return addr, nil
}

// IsRaftLeader reports whether this node is currently the Raft leader for communityID.
func (n *Node) IsRaftLeader(communityID models.CommunityID) bool {
	n.mu.RLock()
	raftNode, ok := n.raftNodes[communityID]
	n.mu.RUnlock()
	if !ok {
		return false
	}
	return raftNode.IsLeader()
}

// HasRemoteCommunity reports whether any OTHER node has announced it is hosting
// communityID. Used by the HTTP handler to avoid Raft split-brain: if another
// node is already leading the community, this node should join as a follower.
func (n *Node) HasRemoteCommunity(communityID models.CommunityID) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.remoteCommunities[communityID]
}
