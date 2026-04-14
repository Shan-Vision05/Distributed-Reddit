package crdt

import (
	"sync"
	"time"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
)

// Positive Negative Counter (PNCounter) implementation
type PNCounter struct {
	mu       sync.RWMutex
	Positive map[models.NodeID]int `json:"positive"`
	Negative map[models.NodeID]int `json:"negative"`
}

func NewPNCounter() *PNCounter {
	return &PNCounter{
		Positive: make(map[models.NodeID]int),
		Negative: make(map[models.NodeID]int),
	}
}

func (c *PNCounter) Increment(nodeID models.NodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Positive[nodeID]++
}

func (c *PNCounter) Decrement(nodeID models.NodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Negative[nodeID]++
}

func (c *PNCounter) Value() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var positiveSum, negativeSum int64
	for _, count := range c.Positive {
		positiveSum += int64(count)
	}

	for _, count := range c.Negative {
		negativeSum += int64(count)
	}

	return positiveSum - negativeSum
}

func (c *PNCounter) Merge(other *PNCounter) {
	c.mu.Lock()
	defer c.mu.Unlock()

	other.mu.RLock()
	defer other.mu.RUnlock()

	for nodeID, count := range other.Positive {
		if count > c.Positive[nodeID] {
			c.Positive[nodeID] = count
		}
	}

	for nodeID, count := range other.Negative {
		if count > c.Negative[nodeID] {
			c.Negative[nodeID] = count
		}
	}
}

// GSet, a grow-only set implementation
type GSet struct {
	mu       sync.RWMutex
	Elements map[string]struct{} `json:"set"`
}

func NewGSet() *GSet {
	return &GSet{
		Elements: make(map[string]struct{}),
	}
}

func (s *GSet) Add(element string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Elements[element] = struct{}{}
}

func (s *GSet) Contains(element string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.Elements[element]
	return exists
}

func (s *GSet) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]string, 0, len(s.Elements))
	for element := range s.Elements {
		result = append(result, element)
	}
	return result
}

func (s *GSet) Merge(other *GSet) {
	s.mu.Lock()
	defer s.mu.Unlock()

	other.mu.RLock()
	defer other.mu.RUnlock()

	for element := range other.Elements {
		s.Elements[element] = struct{}{}
	}
}

// ORSet, an observed-remove set implementation
type ORSet struct {
	mu         sync.RWMutex
	Elements   map[string]map[string]struct{} `json:"orset"`
	Tombstones map[string]map[string]struct{} `json:"tombstones"`
}

func NewORSet() *ORSet {
	return &ORSet{
		Elements:   make(map[string]map[string]struct{}),
		Tombstones: make(map[string]map[string]struct{}),
	}
}

func (s *ORSet) Add(element string, tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Elements[element]; !exists {
		s.Elements[element] = make(map[string]struct{})
	}
	s.Elements[element][tag] = struct{}{}
}

func (s *ORSet) Remove(element string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tags, ok := s.Elements[element]
	if !ok {
		return // element not present, nothing to remove
	}

	if _, exists := s.Tombstones[element]; !exists {
		s.Tombstones[element] = make(map[string]struct{})
	}

	for tag := range tags {
		s.Tombstones[element][tag] = struct{}{}
	}
	delete(s.Elements, element)
}

func (s *ORSet) Contains(value string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tags, ok := s.Elements[value]
	return ok && len(tags) > 0
}

func (s *ORSet) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]string, 0, len(s.Elements))
	for value, tags := range s.Elements {
		if len(tags) > 0 {
			result = append(result, value)
		}
	}
	return result
}

func (s *ORSet) Merge(other *ORSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	// 1. Merge tombstones
	for value, tags := range other.Tombstones {
		if _, ok := s.Tombstones[value]; !ok {
			s.Tombstones[value] = make(map[string]struct{})
		}
		for tag := range tags {
			s.Tombstones[value][tag] = struct{}{}
		}
	}

	// 2. Merge elements, skipping tombstoned tags
	for value, tags := range other.Elements {
		if _, ok := s.Elements[value]; !ok {
			s.Elements[value] = make(map[string]struct{})
		}
		for tag := range tags {
			if tombTags, ok := s.Tombstones[value]; ok {
				if _, dead := tombTags[tag]; dead {
					continue
				}
			}
			s.Elements[value][tag] = struct{}{}
		}
	}

	// 3. Clean up own elements that are now tombstoned
	for value, tombTags := range s.Tombstones {
		if elemTags, ok := s.Elements[value]; ok {
			for tag := range tombTags {
				delete(elemTags, tag)
			}
			if len(elemTags) == 0 {
				delete(s.Elements, value)
			}
		}
	}
}

// LWWRegister, a last-write-wins register implementation

type LWWRegister struct {
	mu        sync.RWMutex
	Value     interface{}   `json:"value"`
	Timestamp time.Time     `json:"timestamp"`
	NodeID    models.NodeID `json:"node_id"`
}

func NewLWWRegister(value interface{}, nodeID models.NodeID) *LWWRegister {
	return &LWWRegister{
		Value:     value,
		Timestamp: time.Now(),
		NodeID:    nodeID,
	}
}

func (r *LWWRegister) Set(value interface{}, nodeID models.NodeID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Value = value
	r.Timestamp = time.Now()
	r.NodeID = nodeID
}

func (r *LWWRegister) Get() interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Value
}

func (r *LWWRegister) Merge(other *LWWRegister) {
	r.mu.Lock()
	defer r.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	if other.Timestamp.After(r.Timestamp) ||
		(other.Timestamp.Equal(r.Timestamp) && other.NodeID > r.NodeID) {
		r.Value = other.Value
		r.Timestamp = other.Timestamp
		r.NodeID = other.NodeID
	}
}

// VoteState tracks all votes for a specific post or comment.
type VoteState struct {
	mu         sync.RWMutex
	TargetHash models.ContentHash             `json:"target_hash"`
	Score      *PNCounter                     `json:"score"`
	UserVotes  map[models.UserID]*LWWRegister `json:"user_votes"`
}

// NewVoteState creates a new vote state for a content hash.
func NewVoteState(targetHash models.ContentHash) *VoteState {
	return &VoteState{
		TargetHash: targetHash,
		Score:      NewPNCounter(),
		UserVotes:  make(map[models.UserID]*LWWRegister),
	}
}

// ApplyVote applies a user's vote, undoing any previous vote first.
func (vs *VoteState) ApplyVote(vote models.Vote, nodeID models.NodeID) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Undo previous vote if exists
	if existing, ok := vs.UserVotes[vote.UserID]; ok {
		if oldVal := existing.Get(); oldVal != nil {
			oldVote := oldVal.(models.VoteType)
			if oldVote == models.Upvote {
				vs.Score.Decrement(nodeID)
			} else {
				vs.Score.Increment(nodeID)
			}
		}
	}

	// Apply new vote
	if vote.Value == models.Upvote {
		vs.Score.Increment(nodeID)
	} else {
		vs.Score.Decrement(nodeID)
	}

	// Record user's vote
	if _, ok := vs.UserVotes[vote.UserID]; !ok {
		vs.UserVotes[vote.UserID] = NewLWWRegister(vote.Value, nodeID)
	} else {
		vs.UserVotes[vote.UserID].Set(vote.Value, nodeID)
	}
}

func (vs *VoteState) GetScore() int64 {
	return vs.Score.Value()
}

// Merge merges another VoteState into this one using CRDT semantics.
func (vs *VoteState) Merge(other *VoteState) {
	if other == nil || vs.TargetHash != other.TargetHash {
		return
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Merge the score counters
	vs.Score.Merge(other.Score)

	// Merge user votes (LWW registers)
	for userID, otherReg := range other.UserVotes {
		if existingReg, ok := vs.UserVotes[userID]; ok {
			existingReg.Merge(otherReg)
		} else {
			// Copy the register
			vs.UserVotes[userID] = &LWWRegister{
				Value:     otherReg.Value,
				Timestamp: otherReg.Timestamp,
				NodeID:    otherReg.NodeID,
			}
		}
	}
}

// Clone creates a deep copy of the VoteState for safe transmission.
func (vs *VoteState) Clone() *VoteState {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	clone := &VoteState{
		TargetHash: vs.TargetHash,
		Score: &PNCounter{
			Positive: make(map[models.NodeID]int),
			Negative: make(map[models.NodeID]int),
		},
		UserVotes: make(map[models.UserID]*LWWRegister),
	}

	for k, v := range vs.Score.Positive {
		clone.Score.Positive[k] = v
	}
	for k, v := range vs.Score.Negative {
		clone.Score.Negative[k] = v
	}

	for userID, reg := range vs.UserVotes {
		clone.UserVotes[userID] = &LWWRegister{
			Value:     reg.Value,
			Timestamp: reg.Timestamp,
			NodeID:    reg.NodeID,
		}
	}

	return clone
}
