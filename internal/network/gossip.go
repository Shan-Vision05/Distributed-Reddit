package network

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/crdt"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/storage"
)

// MessageType identifies the type of gossip message.
type MessageType uint8

const (
	MsgTypePost MessageType = iota + 1
	MsgTypeComment
	MsgTypeVote
	MsgTypeStateSyncRequest
	MsgTypeStateSyncResponse
	MsgTypeCommunityAnnounce // broadcast when a node joins a community
)

// GossipMessage wraps all gossip protocol messages.
type GossipMessage struct {
	Type      MessageType     `json:"type"`
	SenderID  models.NodeID   `json:"sender_id"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// PostPayload is sent when a new post is created.
type PostPayload struct {
	Post *models.Post `json:"post"`
}

// CommentPayload is sent when a new comment is created.
type CommentPayload struct {
	Comment *models.Comment `json:"comment"`
}

// VotePayload is sent when a vote is applied (contains full VoteState for CRDT merge).
type VotePayload struct {
	VoteState *crdt.VoteState `json:"vote_state"`
}

// StateSyncRequestPayload requests full state from a peer.
type StateSyncRequestPayload struct {
	// Empty for now - requests everything
}

// StateSyncResponsePayload contains full state for syncing.
type StateSyncResponsePayload struct {
	Posts      []*models.Post    `json:"posts"`
	Comments   []*models.Comment `json:"comments"`
	VoteStates []*crdt.VoteState `json:"vote_states"`
}

// CommunityAnnouncePayload is broadcast when a node joins or hosts a community.
type CommunityAnnouncePayload struct {
	CommunityID string `json:"community_id"`
	NodeID      string `json:"node_id"`
	RaftAddr    string `json:"raft_addr"`
}

// GossipConfig holds configuration for the gossip node.
type GossipConfig struct {
	NodeID    models.NodeID
	BindAddr  string
	BindPort  int
	JoinAddrs []string // Addresses of existing nodes to join
}

// GossipNode manages gossip-based peer discovery and CRDT sync.
type GossipNode struct {
	config     GossipConfig
	memberlist *memberlist.Memberlist
	store      *storage.ContentStore

	mu       sync.RWMutex
	peers    map[models.NodeID]*memberlist.Node
	delegate *gossipDelegate

	// Callbacks for handling events
	onPeerJoin          func(models.NodeID, string)
	onPeerLeave         func(models.NodeID)
	onCommunityAnnounce func(communityID, nodeID, raftAddr string)
}

// gossipDelegate implements memberlist.Delegate.
type gossipDelegate struct {
	node       *GossipNode
	broadcasts *memberlist.TransmitLimitedQueue
	msgCh      chan []byte
}

// gossipEventDelegate implements memberlist.EventDelegate.
type gossipEventDelegate struct {
	node *GossipNode
}

// NewGossipNode creates a new gossip node.
func NewGossipNode(cfg GossipConfig, store *storage.ContentStore) (*GossipNode, error) {
	gn := &GossipNode{
		config: cfg,
		store:  store,
		peers:  make(map[models.NodeID]*memberlist.Node),
	}

	// Create delegate
	delegate := &gossipDelegate{
		node:  gn,
		msgCh: make(chan []byte, 256),
	}
	gn.delegate = delegate

	// Create memberlist config
	mlConfig := memberlist.DefaultLANConfig()
	mlConfig.Name = string(cfg.NodeID)
	mlConfig.BindAddr = cfg.BindAddr
	mlConfig.BindPort = cfg.BindPort
	mlConfig.AdvertisePort = cfg.BindPort
	mlConfig.Delegate = delegate
	mlConfig.Events = &gossipEventDelegate{node: gn}

	// Reduce logging noise
	mlConfig.LogOutput = log.Writer()

	// Create memberlist
	ml, err := memberlist.Create(mlConfig)
	if err != nil {
		return nil, fmt.Errorf("create memberlist: %w", err)
	}
	gn.memberlist = ml

	// Set up broadcast queue
	delegate.broadcasts = &memberlist.TransmitLimitedQueue{
		NumNodes: func() int {
			return ml.NumMembers()
		},
		RetransmitMult: 3,
	}

	// Start message handler
	go gn.handleMessages()

	return gn, nil
}

// Join attempts to join an existing cluster.
func (gn *GossipNode) Join(addrs []string) (int, error) {
	if len(addrs) == 0 {
		return 0, nil
	}
	return gn.memberlist.Join(addrs)
}

// Leave gracefully leaves the cluster.
func (gn *GossipNode) Leave(timeout time.Duration) error {
	return gn.memberlist.Leave(timeout)
}

// Shutdown shuts down the gossip node.
func (gn *GossipNode) Shutdown() error {
	return gn.memberlist.Shutdown()
}

// Members returns current cluster members.
func (gn *GossipNode) Members() []*memberlist.Node {
	return gn.memberlist.Members()
}

// NumMembers returns the number of members in the cluster.
func (gn *GossipNode) NumMembers() int {
	return gn.memberlist.NumMembers()
}

// LocalNode returns the local node.
func (gn *GossipNode) LocalNode() *memberlist.Node {
	return gn.memberlist.LocalNode()
}

// SetOnPeerJoin sets callback for peer join events.
func (gn *GossipNode) SetOnPeerJoin(fn func(models.NodeID, string)) {
	gn.mu.Lock()
	defer gn.mu.Unlock()
	gn.onPeerJoin = fn
}

// SetOnPeerLeave sets callback for peer leave events.
func (gn *GossipNode) SetOnPeerLeave(fn func(models.NodeID)) {
	gn.mu.Lock()
	defer gn.mu.Unlock()
	gn.onPeerLeave = fn
}

// SetOnCommunityAnnounce sets the callback fired when another node broadcasts
// that it has joined a community. Receives (communityID, nodeID, raftAddr).
func (gn *GossipNode) SetOnCommunityAnnounce(fn func(communityID, nodeID, raftAddr string)) {
	gn.mu.Lock()
	defer gn.mu.Unlock()
	gn.onCommunityAnnounce = fn
}

// BroadcastCommunityAnnounce tells peers that this node is hosting communityID
// and can be reached at raftAddr for Raft consensus.
func (gn *GossipNode) BroadcastCommunityAnnounce(communityID, nodeID, raftAddr string) error {
	payload, err := json.Marshal(CommunityAnnouncePayload{
		CommunityID: communityID,
		NodeID:      nodeID,
		RaftAddr:    raftAddr,
	})
	if err != nil {
		return fmt.Errorf("marshal community announce: %w", err)
	}
	msg := GossipMessage{
		Type:      MsgTypeCommunityAnnounce,
		SenderID:  gn.config.NodeID,
		Timestamp: time.Now(),
		Payload:   payload,
	}
	return gn.broadcast(msg)
}

// BroadcastPost broadcasts a new post to all peers.
func (gn *GossipNode) BroadcastPost(post *models.Post) error {
	payload, err := json.Marshal(PostPayload{Post: post})
	if err != nil {
		return fmt.Errorf("marshal post payload: %w", err)
	}

	msg := GossipMessage{
		Type:      MsgTypePost,
		SenderID:  gn.config.NodeID,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	return gn.broadcast(msg)
}

// BroadcastComment broadcasts a new comment to all peers.
func (gn *GossipNode) BroadcastComment(comment *models.Comment) error {
	payload, err := json.Marshal(CommentPayload{Comment: comment})
	if err != nil {
		return fmt.Errorf("marshal comment payload: %w", err)
	}

	msg := GossipMessage{
		Type:      MsgTypeComment,
		SenderID:  gn.config.NodeID,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	return gn.broadcast(msg)
}

// BroadcastVoteState broadcasts a vote state update for CRDT merge.
func (gn *GossipNode) BroadcastVoteState(vs *crdt.VoteState) error {
	payload, err := json.Marshal(VotePayload{VoteState: vs})
	if err != nil {
		return fmt.Errorf("marshal vote payload: %w", err)
	}

	msg := GossipMessage{
		Type:      MsgTypeVote,
		SenderID:  gn.config.NodeID,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	return gn.broadcast(msg)
}

// RequestStateSync requests full state sync from a specific peer.
func (gn *GossipNode) RequestStateSync(peerAddr string) error {
	payload, err := json.Marshal(StateSyncRequestPayload{})
	if err != nil {
		return fmt.Errorf("marshal sync request: %w", err)
	}

	msg := GossipMessage{
		Type:      MsgTypeStateSyncRequest,
		SenderID:  gn.config.NodeID,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	// Find the node and send directly
	for _, member := range gn.memberlist.Members() {
		if member.Address() == peerAddr {
			return gn.memberlist.SendReliable(member, data)
		}
	}

	return fmt.Errorf("peer not found: %s", peerAddr)
}

// broadcast sends a message to all peers via the broadcast queue.
func (gn *GossipNode) broadcast(msg GossipMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	gn.delegate.broadcasts.QueueBroadcast(&broadcast{msg: data})
	return nil
}

// handleMessages processes incoming gossip messages.
func (gn *GossipNode) handleMessages() {
	for data := range gn.delegate.msgCh {
		var msg GossipMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		// Don't process our own messages
		if msg.SenderID == gn.config.NodeID {
			continue
		}

		switch msg.Type {
		case MsgTypePost:
			gn.handlePostMessage(msg)
		case MsgTypeComment:
			gn.handleCommentMessage(msg)
		case MsgTypeVote:
			gn.handleVoteMessage(msg)
		case MsgTypeStateSyncRequest:
			gn.handleStateSyncRequest(msg)
		case MsgTypeStateSyncResponse:
			gn.handleStateSyncResponse(msg)
		case MsgTypeCommunityAnnounce:
			gn.handleCommunityAnnounce(msg)
		}
	}
}

func (gn *GossipNode) handlePostMessage(msg GossipMessage) {
	var payload PostPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	if payload.Post == nil {
		return
	}

	// Check if we already have this post (content-addressed, so hash is deterministic)
	if gn.store.HasPost(payload.Post.Hash) {
		return
	}

	// Store the post
	_, _ = gn.store.StorePost(payload.Post)
}

func (gn *GossipNode) handleCommentMessage(msg GossipMessage) {
	var payload CommentPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	if payload.Comment == nil {
		return
	}

	// Check if we already have this comment
	if gn.store.HasComment(payload.Comment.Hash) {
		return
	}

	// Store the comment
	_, _ = gn.store.StoreComment(payload.Comment)
}

func (gn *GossipNode) handleVoteMessage(msg GossipMessage) {
	var payload VotePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	if payload.VoteState == nil {
		return
	}

	// Merge the incoming VoteState with our local one
	_ = gn.store.MergeVoteState(payload.VoteState)
}

func (gn *GossipNode) handleStateSyncRequest(msg GossipMessage) {
	// Build response with all our state
	response := StateSyncResponsePayload{
		Posts:      gn.store.GetAllPosts(),
		Comments:   gn.store.GetAllComments(),
		VoteStates: gn.store.GetAllVoteStates(),
	}

	payload, err := json.Marshal(response)
	if err != nil {
		return
	}

	respMsg := GossipMessage{
		Type:      MsgTypeStateSyncResponse,
		SenderID:  gn.config.NodeID,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	data, err := json.Marshal(respMsg)
	if err != nil {
		return
	}

	// Send directly to the requester
	for _, member := range gn.memberlist.Members() {
		if models.NodeID(member.Name) == msg.SenderID {
			_ = gn.memberlist.SendReliable(member, data)
			break
		}
	}
}

func (gn *GossipNode) handleStateSyncResponse(msg GossipMessage) {
	var payload StateSyncResponsePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	// Import all posts
	for _, post := range payload.Posts {
		if !gn.store.HasPost(post.Hash) {
			_, _ = gn.store.StorePost(post)
		}
	}

	// Import all comments
	for _, comment := range payload.Comments {
		if !gn.store.HasComment(comment.Hash) {
			_, _ = gn.store.StoreComment(comment)
		}
	}

	// Merge all vote states
	for _, vs := range payload.VoteStates {
		_ = gn.store.MergeVoteState(vs)
	}
}

func (gn *GossipNode) handleCommunityAnnounce(msg GossipMessage) {
	var payload CommunityAnnouncePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}
	gn.mu.RLock()
	callback := gn.onCommunityAnnounce
	gn.mu.RUnlock()
	if callback != nil {
		callback(payload.CommunityID, payload.NodeID, payload.RaftAddr)
	}
}

// --- memberlist.Delegate implementation ---

func (d *gossipDelegate) NodeMeta(limit int) []byte {
	return nil
}

func (d *gossipDelegate) NotifyMsg(data []byte) {
	// Non-blocking send to message channel
	select {
	case d.msgCh <- data:
	default:
		// Channel full, drop message
	}
}

func (d *gossipDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return d.broadcasts.GetBroadcasts(overhead, limit)
}

func (d *gossipDelegate) LocalState(join bool) []byte {
	return nil
}

func (d *gossipDelegate) MergeRemoteState(buf []byte, join bool) {
	// We use explicit state sync requests instead
}

// --- memberlist.EventDelegate implementation ---

func (e *gossipEventDelegate) NotifyJoin(node *memberlist.Node) {
	nodeID := models.NodeID(node.Name)

	e.node.mu.Lock()
	e.node.peers[nodeID] = node
	callback := e.node.onPeerJoin
	e.node.mu.Unlock()

	if callback != nil {
		callback(nodeID, node.Address())
	}
}

func (e *gossipEventDelegate) NotifyLeave(node *memberlist.Node) {
	nodeID := models.NodeID(node.Name)

	e.node.mu.Lock()
	delete(e.node.peers, nodeID)
	callback := e.node.onPeerLeave
	e.node.mu.Unlock()

	if callback != nil {
		callback(nodeID)
	}
}

func (e *gossipEventDelegate) NotifyUpdate(node *memberlist.Node) {
	nodeID := models.NodeID(node.Name)

	e.node.mu.Lock()
	e.node.peers[nodeID] = node
	e.node.mu.Unlock()
}

// --- broadcast type for memberlist.TransmitLimitedQueue ---

type broadcast struct {
	msg []byte
}

func (b *broadcast) Invalidates(other memberlist.Broadcast) bool {
	return false
}

func (b *broadcast) Message() []byte {
	return b.msg
}

func (b *broadcast) Finished() {
	// No-op
}
