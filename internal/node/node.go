package node

import (
	"fmt"
	"math/rand"
	"os"
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

	mu          sync.RWMutex
	store       *storage.ContentStore // shared store: used by both gossip and all community managers
	communities map[models.CommunityID]*community.Manager
	raftNodes   map[models.CommunityID]*consensus.RaftNode
	raftAddrs   map[models.CommunityID]string // this node's Raft TCP bind address per community
}

// NodeConfig holds optional configuration for NewNodeWithConfig.
// Zero-values mean "pick a sensible default".
type NodeConfig struct {
	GossipPort  int      // 0 = pick a random port in 10000-19999
	GossipPeers []string // seed addresses ("host:port") to join at startup
}

// NewNode creates a node with a random gossip port and no pre-configured peers.
// Existing callers (tests, cmd/dreddit) continue to work unchanged.
func NewNode(nodeID models.NodeID, bindAddr string) (*Node, error) {
	return NewNodeWithConfig(nodeID, bindAddr, NodeConfig{})
}

// NewNodeWithConfig is the full constructor that accepts explicit gossip config.
func NewNodeWithConfig(nodeID models.NodeID, bindAddr string, cfg NodeConfig) (*Node, error) {
	dhtNode := dht.NewCommunityDHT(dht.DHTConfig{VirtualNodes: 150, ReplicationFactor: 3})
	dhtNode.AddNode(&models.NodeInfo{ID: nodeID, Address: bindAddr, IsAlive: true})

	store, _ := storage.NewContentStore("")
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
		NodeID:      nodeID,
		DHT:         dhtNode,
		Gossip:      gossipNode,
		store:       store,
		communities: make(map[models.CommunityID]*community.Manager),
		raftNodes:   make(map[models.CommunityID]*consensus.RaftNode),
		raftAddrs:   make(map[models.CommunityID]string),
	}

	// Keep the DHT ring in sync with gossip membership changes.
	gossipNode.SetOnPeerJoin(func(id models.NodeID, addr string) {
		dhtNode.AddNode(&models.NodeInfo{ID: id, Address: addr, IsAlive: true})
	})
	gossipNode.SetOnPeerLeave(func(id models.NodeID) {
		dhtNode.RemoveNode(id)
	})

	// When a peer announces it has joined a community, add it to our DHT view.
	// If this node is the Raft leader for that community, add the peer as a voter.
	gossipNode.SetOnCommunityAnnounce(func(communityID, peerNodeID, raftAddr string) {
		cid := models.CommunityID(communityID)
		dhtNode.AssignCommunity(models.CommunityID(communityID), []models.NodeID{models.NodeID(peerNodeID)})

		n.mu.RLock()
		raftNode := n.raftNodes[cid]
		n.mu.RUnlock()

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

	files, err := os.ReadDir(".")
	if err == nil {
		for _, f := range files {
			prefix := fmt.Sprintf("data_%s_", nodeID)
			if strings.HasPrefix(f.Name(), prefix) {
				commID := strings.TrimPrefix(f.Name(), prefix)
				n.JoinCommunity(models.CommunityID(commID))
			}
		}
	}

	return n, nil
}

func (n *Node) JoinCommunity(communityID models.CommunityID) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, exists := n.communities[communityID]; exists {
		return fmt.Errorf("already a member of community %s", communityID)
	}

	dataDir := fmt.Sprintf("data_%s_%s", n.NodeID, communityID)
	// n.store is the shared content store used by both gossip and all community managers.
	// dataDir is used only for Raft log persistence.
	raftAddr := fmt.Sprintf("127.0.0.1:%d", 20000+rand.Intn(10000))
	raftCfg := consensus.RaftConfig{
		NodeID:      n.NodeID,
		CommunityID: communityID,
		BindAddr:    raftAddr,
		Bootstrap:   true,
		DataDir:     dataDir,
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
	// and the leader can add this node as a Raft voter (Phase 5).
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

	dataDir := fmt.Sprintf("data_%s_%s", n.NodeID, communityID)
	raftAddr := fmt.Sprintf("127.0.0.1:%d", 20000+rand.Intn(10000))
	raftCfg := consensus.RaftConfig{
		NodeID:      n.NodeID,
		CommunityID: communityID,
		BindAddr:    raftAddr,
		Bootstrap:   false, // follower: do NOT bootstrap a new cluster
		DataDir:     dataDir,
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
