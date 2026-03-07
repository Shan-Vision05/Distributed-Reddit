package consensus

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"

	"github.com/Shan-Vision05/DReddit/internal/models"
)

type RaftNode struct {
	raft        *raft.Raft
	fsm         *ModerationFSM
	communityID models.CommunityID
	nodeID      models.NodeID
}

type RaftConfig struct {
	NodeID      models.NodeID
	CommunityID models.CommunityID
	BindAddr    string
	DataDir     string
	Bootstrap   bool // true if this is the initial leader
	Peers       []raft.Server
}

func NewRaftNode(cfg RaftConfig) (*RaftNode, error) {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(cfg.NodeID)
	config.SnapshotThreshold = 1024

	logStore := raft.NewInmemStore()
	stableStore := raft.NewInmemStore()
	snapshotStore := raft.NewInmemSnapshotStore()

	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve bind addr: %w", err)
	}
	transport, err := raft.NewTCPTransport(cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	fsm := NewModerationFSM()

	r, err := raft.NewRaft(config, fsm, logStore, stableStore, snapshotStore, transport)
	if err != nil {
		return nil, fmt.Errorf("create raft: %w", err)
	}

	if cfg.Bootstrap {
		servers := []raft.Server{
			{
				ID:      raft.ServerID(cfg.NodeID),
				Address: transport.LocalAddr(),
			},
		}
		servers = append(servers, cfg.Peers...)
		r.BootstrapCluster(raft.Configuration{Servers: servers})
	}

	return &RaftNode{
		raft:        r,
		fsm:         fsm,
		communityID: cfg.CommunityID,
		nodeID:      cfg.NodeID,
	}, nil
}

func NewInMemRaftNode(nodeID models.NodeID, communityID models.CommunityID, transport raft.Transport) (*RaftNode, error) {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(nodeID)
	config.SnapshotThreshold = 1024
	config.HeartbeatTimeout = 50 * time.Millisecond
	config.ElectionTimeout = 50 * time.Millisecond
	config.LeaderLeaseTimeout = 50 * time.Millisecond
	config.CommitTimeout = 5 * time.Millisecond

	logStore := raft.NewInmemStore()
	stableStore := raft.NewInmemStore()
	snapshotStore := raft.NewInmemSnapshotStore()

	fsm := NewModerationFSM()

	r, err := raft.NewRaft(config, fsm, logStore, stableStore, snapshotStore, transport)
	if err != nil {
		return nil, fmt.Errorf("create raft: %w", err)
	}

	return &RaftNode{
		raft:        r,
		fsm:         fsm,
		communityID: communityID,
		nodeID:      nodeID,
	}, nil
}

func (rn *RaftNode) Propose(action models.ModerationAction) error {
	data, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("marshal action: %w", err)
	}

	f := rn.raft.Apply(data, 5*time.Second)
	if err := f.Error(); err != nil {
		return fmt.Errorf("raft apply: %w", err)
	}

	if resp := f.Response(); resp != nil {
		if e, ok := resp.(error); ok {
			return e
		}
	}
	return nil
}

func (rn *RaftNode) GetLog() []models.ModerationAction {
	return rn.fsm.GetLog()
}

func (rn *RaftNode) IsLeader() bool {
	return rn.raft.State() == raft.Leader
}

func (rn *RaftNode) LeaderAddr() string {
	addr, _ := rn.raft.LeaderWithID()
	return string(addr)
}

func (rn *RaftNode) State() string {
	return rn.raft.State().String()
}

func (rn *RaftNode) AddVoter(id models.NodeID, addr string) error {
	f := rn.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 5*time.Second)
	return f.Error()
}

func (rn *RaftNode) RemoveServer(id models.NodeID) error {
	f := rn.raft.RemoveServer(raft.ServerID(id), 0, 5*time.Second)
	return f.Error()
}

func (rn *RaftNode) Shutdown() error {
	f := rn.raft.Shutdown()
	return f.Error()
}

func (rn *RaftNode) NodeID() models.NodeID {
	return rn.nodeID
}

func (rn *RaftNode) CommunityID() models.CommunityID {
	return rn.communityID
}

func DataDir(baseDir string, communityID models.CommunityID) string {
	return filepath.Join(baseDir, "raft", string(communityID))
}
