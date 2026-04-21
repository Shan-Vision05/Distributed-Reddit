package tests

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/crdt"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/storage"
)

type evalReplica struct {
	id     models.NodeID
	region string
	store  *storage.ContentStore
	group  string
}

type evalEvent struct {
	at      time.Duration
	kind    string
	replica int
	vote    models.Vote
	state   *crdt.VoteState
	sender  int
}

type evalSimulation struct {
	replicas          []*evalReplica
	targetHash        models.ContentHash
	currentTime       time.Duration
	queue             []evalEvent
	lastBroadcastSigs map[int]string
	latencyBetween    func(from, to int) time.Duration
	canCommunicate    func(from, to int, at time.Duration) bool
}

func newEvalSimulation(t *testing.T) *evalSimulation {
	t.Helper()

	regions := []struct {
		name  string
		group string
	}{
		{name: "us-east", group: "west-cluster"},
		{name: "us-west", group: "west-cluster"},
		{name: "eu-central", group: "east-cluster"},
		{name: "ap-southeast", group: "east-cluster"},
	}

	replicas := make([]*evalReplica, 0, 20)
	sharedPost := &models.Post{
		CommunityID: "eval-bench",
		AuthorID:    "seed-author",
		Title:       "Evaluation Seed",
		Body:        "Synthetic convergence anchor",
		CreatedAt:   time.Unix(1700000000, 0),
	}

	var targetHash models.ContentHash
	for _, region := range regions {
		for i := 0; i < 5; i++ {
			store, err := storage.NewContentStore("")
			if err != nil {
				t.Fatalf("create store: %v", err)
			}
			hash, err := store.StorePost(&models.Post{
				CommunityID: sharedPost.CommunityID,
				AuthorID:    sharedPost.AuthorID,
				Title:       sharedPost.Title,
				Body:        sharedPost.Body,
				CreatedAt:   sharedPost.CreatedAt,
			})
			if err != nil {
				t.Fatalf("store seed post: %v", err)
			}
			targetHash = hash
			replicas = append(replicas, &evalReplica{
				id:     models.NodeID(fmt.Sprintf("%s-%d", region.name, i+1)),
				region: region.name,
				store:  store,
				group:  region.group,
			})
		}
	}

	latencies := map[string]map[string]time.Duration{
		"us-east": {
			"us-east": 5 * time.Millisecond,
			"us-west": 70 * time.Millisecond,
			"eu-central": 95 * time.Millisecond,
			"ap-southeast": 180 * time.Millisecond,
		},
		"us-west": {
			"us-east": 70 * time.Millisecond,
			"us-west": 5 * time.Millisecond,
			"eu-central": 130 * time.Millisecond,
			"ap-southeast": 210 * time.Millisecond,
		},
		"eu-central": {
			"us-east": 95 * time.Millisecond,
			"us-west": 130 * time.Millisecond,
			"eu-central": 5 * time.Millisecond,
			"ap-southeast": 160 * time.Millisecond,
		},
		"ap-southeast": {
			"us-east": 180 * time.Millisecond,
			"us-west": 210 * time.Millisecond,
			"eu-central": 160 * time.Millisecond,
			"ap-southeast": 5 * time.Millisecond,
		},
	}

	return &evalSimulation{
		replicas:   replicas,
		targetHash: targetHash,
		lastBroadcastSigs: make(map[int]string),
		latencyBetween: func(from, to int) time.Duration {
			return latencies[replicas[from].region][replicas[to].region]
		},
		canCommunicate: func(from, to int, at time.Duration) bool {
			return true
		},
	}
}

func (s *evalSimulation) enqueue(event evalEvent) {
	s.queue = append(s.queue, event)
	sort.Slice(s.queue, func(i, j int) bool {
		if s.queue[i].at == s.queue[j].at {
			return s.queue[i].replica < s.queue[j].replica
		}
		return s.queue[i].at < s.queue[j].at
	})
}

func (s *evalSimulation) currentSignature(replicaIndex int) string {
	state := s.replicas[replicaIndex].store.GetVoteState(s.targetHash)
	if state == nil {
		return ""
	}
	data, _ := json.Marshal(state)
	return string(data)
}

func (s *evalSimulation) scheduleBroadcast(replicaIndex int) {
	signature := s.currentSignature(replicaIndex)
	if signature == s.lastBroadcastSigs[replicaIndex] {
		return
	}
	s.lastBroadcastSigs[replicaIndex] = signature
	for peerIndex := range s.replicas {
		if peerIndex == replicaIndex {
			continue
		}
		if !s.canCommunicate(replicaIndex, peerIndex, s.currentTime) {
			continue
		}
		s.enqueue(evalEvent{
			at:      s.currentTime + s.latencyBetween(replicaIndex, peerIndex),
			kind:    "deliver",
			replica: peerIndex,
			state:   s.replicas[replicaIndex].store.GetVoteState(s.targetHash),
			sender:  replicaIndex,
		})
	}
}

func (s *evalSimulation) scheduleFullSync(at time.Duration) {
	s.enqueue(evalEvent{at: at, kind: "full-sync", replica: -1})
}

func (s *evalSimulation) scheduleVote(at time.Duration, replicaIndex int, userID string, value models.VoteType) {
	s.enqueue(evalEvent{
		at:      at,
		kind:    "vote",
		replica: replicaIndex,
		vote: models.Vote{
			TargetHash: s.targetHash,
			UserID:     models.UserID(userID),
			Value:      value,
			Timestamp:  time.Unix(1700000000, int64(at)),
		},
	})
}

func (s *evalSimulation) step(t *testing.T) bool {
	t.Helper()
	if len(s.queue) == 0 {
		return false
	}
	event := s.queue[0]
	s.queue = s.queue[1:]
	s.currentTime = event.at

	switch event.kind {
	case "vote":
		if err := s.replicas[event.replica].store.ApplyVote(event.vote, s.replicas[event.replica].id); err != nil {
			t.Fatalf("apply vote on %s: %v", s.replicas[event.replica].id, err)
		}
		s.scheduleBroadcast(event.replica)
	case "deliver":
		if !s.canCommunicate(event.sender, event.replica, event.at) {
			return true
		}
		before := s.currentSignature(event.replica)
		incoming := event.state
		if incoming == nil {
			t.Fatalf("sender %s missing vote state", s.replicas[event.sender].id)
		}
		if err := s.replicas[event.replica].store.MergeVoteState(incoming); err != nil {
			t.Fatalf("merge state into %s: %v", s.replicas[event.replica].id, err)
		}
		after := s.currentSignature(event.replica)
		if after != before {
			s.scheduleBroadcast(event.replica)
		}
	case "full-sync":
		s.lastBroadcastSigs = make(map[int]string)
		for replicaIndex := range s.replicas {
			s.scheduleBroadcast(replicaIndex)
		}
	default:
		t.Fatalf("unknown event kind %q", event.kind)
	}

	return true
}

func (s *evalSimulation) allScoresEqual(expected int64) bool {
	for _, replica := range s.replicas {
		score, err := replica.store.GetVoteScore(s.targetHash)
		if err != nil || score != expected {
			return false
		}
	}
	return true
}

func (s *evalSimulation) scoreAt(replicaIndex int) int64 {
	score, _ := s.replicas[replicaIndex].store.GetVoteScore(s.targetHash)
	return score
}

func TestEvaluationWANConvergenceAcrossRegions(t *testing.T) {
	sim := newEvalSimulation(t)
	votes := []struct {
		replica int
		userID  string
		value   models.VoteType
		at      time.Duration
	}{
		{replica: 0, userID: "user-a", value: models.Upvote, at: 0 * time.Millisecond},
		{replica: 2, userID: "user-b", value: models.Upvote, at: 15 * time.Millisecond},
		{replica: 5, userID: "user-c", value: models.Upvote, at: 30 * time.Millisecond},
		{replica: 7, userID: "user-d", value: models.Upvote, at: 45 * time.Millisecond},
		{replica: 10, userID: "user-e", value: models.Downvote, at: 60 * time.Millisecond},
		{replica: 12, userID: "user-f", value: models.Upvote, at: 75 * time.Millisecond},
		{replica: 15, userID: "user-g", value: models.Upvote, at: 90 * time.Millisecond},
		{replica: 18, userID: "user-h", value: models.Downvote, at: 105 * time.Millisecond},
	}
	for _, vote := range votes {
		sim.scheduleVote(vote.at, vote.replica, vote.userID, vote.value)
	}

	const expectedScore int64 = 4
	lastVoteAt := votes[len(votes)-1].at
	convergedAt := time.Duration(-1)
	for sim.step(t) {
		if sim.currentTime >= lastVoteAt && sim.allScoresEqual(expectedScore) {
			convergedAt = sim.currentTime
			break
		}
	}

	if convergedAt < 0 {
		t.Fatalf("replicas never converged to score %d", expectedScore)
	}

	convergenceTime := convergedAt - lastVoteAt
	if convergenceTime > 2*time.Second {
		t.Fatalf("cross-region convergence too slow: got %v, want <= 2s", convergenceTime)
	}

	t.Logf("METRIC cross_region_convergence_ms=%d replicas=%d expected_score=%d threshold_ms=%d", convergenceTime.Milliseconds(), len(sim.replicas), expectedScore, (2 * time.Second).Milliseconds())
	t.Logf("cross-region convergence after final write: %v across %d replicas", convergenceTime, len(sim.replicas))
}

func TestEvaluationPartitionHealConvergence20Replicas(t *testing.T) {
	sim := newEvalSimulation(t)
	healAt := 400 * time.Millisecond
	sim.canCommunicate = func(from, to int, at time.Duration) bool {
		if at >= healAt {
			return true
		}
		return sim.replicas[from].group == sim.replicas[to].group
	}
	sim.scheduleFullSync(healAt)

	for replicaIndex := 0; replicaIndex < 6; replicaIndex++ {
		sim.scheduleVote(time.Duration(replicaIndex*20)*time.Millisecond, replicaIndex, fmt.Sprintf("west-user-%d", replicaIndex), models.Upvote)
	}
	for replicaIndex := 10; replicaIndex < 14; replicaIndex++ {
		sim.scheduleVote(time.Duration((replicaIndex-10)*25)*time.Millisecond, replicaIndex, fmt.Sprintf("east-user-%d", replicaIndex), models.Downvote)
	}

	divergedBeforeHeal := false
	convergedAt := time.Duration(-1)
	const expectedScore int64 = 2

	for sim.step(t) {
		if sim.currentTime < healAt {
			westScore := sim.scoreAt(0)
			eastScore := sim.scoreAt(10)
			if westScore != eastScore {
				divergedBeforeHeal = true
			}
			continue
		}
		if sim.allScoresEqual(expectedScore) {
			convergedAt = sim.currentTime
			break
		}
	}

	if !divergedBeforeHeal {
		t.Fatal("partition scenario never produced divergent scores before heal")
	}
	if convergedAt < 0 {
		t.Fatalf("replicas never converged to score %d after heal", expectedScore)
	}

	healConvergence := convergedAt - healAt
	if healConvergence > 1500*time.Millisecond {
		t.Fatalf("post-heal convergence too slow: got %v, want <= 1.5s", healConvergence)
	}

	t.Logf("METRIC partition_heal_convergence_ms=%d replicas=%d expected_score=%d threshold_ms=%d heal_at_ms=%d", healConvergence.Milliseconds(), len(sim.replicas), expectedScore, (1500 * time.Millisecond).Milliseconds(), healAt.Milliseconds())
	t.Logf("partition heal convergence: %v across %d replicas", healConvergence, len(sim.replicas))
}