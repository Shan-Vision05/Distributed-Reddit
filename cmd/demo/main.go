package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/raft"

	"github.com/Shan-Vision05/Distributed-Reddit/internal/consensus"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/crdt"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/models"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/network"
	"github.com/Shan-Vision05/Distributed-Reddit/internal/storage"
)

func main() {
	fmt.Println("|            DReddit — Interactive Demo            |")
	fmt.Println()

	demoStorage()
	demoCRDTs()
	demoRaft()
	demoNetwork()

	fmt.Println()
	fmt.Println("  All demos completed successfully!")
}

func demoStorage() {
	section("1. Content-Addressed Storage")

	store, err := storage.NewContentStore("")
	check(err, "create store")

	// Store posts
	post1 := &models.Post{
		CommunityID: "golang",
		AuthorID:    "Tony Stark",
		Title:       "Jarvis, where is my suit?",
		Body:        "Jarvis is being very lazy today. I need my suit for the next Avengers mission!",
		CreatedAt:   time.Now(),
	}
	hash1, err := store.StorePost(post1)
	check(err, "store post 1")
	info("Stored post 1", "hash=%s", truncHash(hash1))

	post2 := &models.Post{
		CommunityID: "golang",
		AuthorID:    "Peter Parker",
		Title:       "Mr. Stark, I don't feel so good",
		Body:        "Something weird is happening. Also, Go is confusing.",
		CreatedAt:   time.Now(),
	}
	hash2, err := store.StorePost(post2)
	check(err, "store post 2")
	info("Stored post 2", "hash=%s", truncHash(hash2))

	post3 := &models.Post{
		CommunityID: "golang",
		AuthorID:    "Bruce Banner",
		Title:       "Go vs Hulk",
		Body:        "Smash Smash!",
		CreatedAt:   time.Now(),
	}
	hash3, err := store.StorePost(post3)
	check(err, "store post 3")
	info("Stored post 3", "hash=%s", truncHash(hash3))

	post4 := &models.Post{
		CommunityID: "avengers",
		AuthorID:    "Nick Fury",
		Title:       "Assembling the team, again",
		Body:        "Why does nobody answer their pager anymore?",
		CreatedAt:   time.Now(),
	}
	hash4, err := store.StorePost(post4)
	check(err, "store post 4")
	info("Stored post 4", "hash=%s (community: avengers)", truncHash(hash4))

	got, err := store.GetPost(hash1)
	check(err, "get post")
	info("Retrieved post 1", `"%s" by %s`, got.Title, got.AuthorID)

	duplicate := &models.Post{
		CommunityID: post1.CommunityID,
		AuthorID:    post1.AuthorID,
		Title:       post1.Title,
		Body:        post1.Body,
		CreatedAt:   post1.CreatedAt,
	}
	dupHash := duplicate.ComputeHash()
	if hash1 == dupHash {
		info("Verified", "same content produces same hash (content-addressed)")
	}

	c1, err := store.StoreComment(&models.Comment{
		PostHash:  hash1,
		AuthorID:  "Peter Parker",
		Body:      "On it, Mr. Stark! I think I saw it in the lab.",
		CreatedAt: time.Now(),
	})
	check(err, "store comment 1")
	info("Comment on post 1", "hash=%s by Peter Parker", truncHash(c1))

	c2, err := store.StoreComment(&models.Comment{
		PostHash:  hash1,
		AuthorID:  "Pepper Potts",
		Body:      "Tony, you left it in the garage. Again.",
		CreatedAt: time.Now(),
	})
	check(err, "store comment 2")
	info("Comment on post 1", "hash=%s by Pepper Potts", truncHash(c2))

	c3, err := store.StoreComment(&models.Comment{
		PostHash:  hash2,
		AuthorID:  "Tony Stark",
		Body:      "Kid, you're fine.",
		CreatedAt: time.Now(),
	})
	check(err, "store comment 3")
	info("Comment on post 2", "hash=%s by Tony Stark", truncHash(c3))

	c4, err := store.StoreComment(&models.Comment{
		PostHash:  hash3,
		AuthorID:  "Tony Stark",
		Body:      "Banner, don't.",
		CreatedAt: time.Now(),
	})
	check(err, "store comment 4")
	info("Comment on post 3", "hash=%s by Tony Stark", truncHash(c4))

	c5, err := store.StoreComment(&models.Comment{
		PostHash:  hash3,
		AuthorID:  "Thor",
		Body:      "I understood none of this.",
		CreatedAt: time.Now(),
	})
	check(err, "store comment 5")
	info("Comment on post 3", "hash=%s by Thor", truncHash(c5))

	_, err = store.StoreComment(&models.Comment{
		PostHash:  hash4,
		AuthorID:  "Captain America",
		Body:      "Sorry Nick, I was frozen. What did I miss?",
		CreatedAt: time.Now(),
	})
	check(err, "store comment 6")

	golangPosts := store.GetCommunityPosts("golang")
	info("Community 'golang'", "%d post(s)", len(golangPosts))

	avengersPosts := store.GetCommunityPosts("avengers")
	info("Community 'avengers'", "%d post(s)", len(avengersPosts))

	info("Post 1 comments", "%d comment(s)", len(store.GetPostComments(hash1)))
	info("Post 2 comments", "%d comment(s)", len(store.GetPostComments(hash2)))
	info("Post 3 comments", "%d comment(s)", len(store.GetPostComments(hash3)))
	info("Post 4 comments", "%d comment(s)", len(store.GetPostComments(hash4)))

	fmt.Println()
}

func demoCRDTs() {
	section("2. CRDT Conflict-Free Data Structures")

	subsection("PNCounter — distributed counter")
	c1 := crdt.NewPNCounter()
	c2 := crdt.NewPNCounter()

	c1.Increment("node-A")
	c1.Increment("node-A")
	c2.Increment("node-B")
	c2.Decrement("node-B")

	info("Node A counter", "value=%d (2 increments)", c1.Value())
	info("Node B counter", "value=%d (1 inc, 1 dec)", c2.Value())

	c1.Merge(c2)
	info("After merge", "value=%d (both nodes' ops combined)", c1.Value())

	subsection("GSet — grow-only set (e.g., user registrations)")
	g1 := crdt.NewGSet()
	g2 := crdt.NewGSet()

	g1.Add("alice")
	g1.Add("bob")
	g2.Add("bob")
	g2.Add("charlie")

	info("Set 1", "%v", g1.List())
	info("Set 2", "%v", g2.List())
	g1.Merge(g2)
	info("After merge", "%v (union, no duplicates)", g1.List())

	subsection("ORSet — add & remove with conflict resolution")
	or1 := crdt.NewORSet()
	or2 := crdt.NewORSet()

	or1.Add("post-1", "tag-a")
	or1.Add("post-2", "tag-b")
	or2.Add("post-1", "tag-c")
	or2.Remove("post-1") // removes tag-c only

	info("Node 1", "contains post-1: %v", or1.Contains("post-1"))
	info("Node 2", "contains post-1: %v (removed locally)", or2.Contains("post-1"))

	or1.Merge(or2)
	info("After merge", "contains post-1: %v (add wins — tag-a survived)", or1.Contains("post-1"))

	subsection("VoteState — Reddit-style upvote/downvote")
	store, _ := storage.NewContentStore("")
	post := &models.Post{
		CommunityID: "demo",
		AuthorID:    "alice",
		Title:       "Vote on me",
		Body:        "Testing votes",
		CreatedAt:   time.Now(),
	}
	hash, _ := store.StorePost(post)

	store.ApplyVote(models.Vote{TargetHash: hash, UserID: "user1", Value: models.Upvote, Timestamp: time.Now()}, "node-A")
	score, _ := store.GetVoteScore(hash)
	info("user1 upvotes", "score=%d", score)

	store.ApplyVote(models.Vote{TargetHash: hash, UserID: "user2", Value: models.Upvote, Timestamp: time.Now()}, "node-A")
	score, _ = store.GetVoteScore(hash)
	info("user2 upvotes", "score=%d", score)

	store.ApplyVote(models.Vote{TargetHash: hash, UserID: "user1", Value: models.Downvote, Timestamp: time.Now().Add(time.Second)}, "node-A")
	score, _ = store.GetVoteScore(hash)
	info("user1 changes to downvote", "score=%d (last-writer-wins per user)", score)

	fmt.Println()
}

func demoRaft() {
	section("3. Raft Consensus — Moderation Log (3-node cluster)")

	nodeIDs := []string{"node-A", "node-B", "node-C"}
	transports := make([]raft.LoopbackTransport, 3)
	addrs := make([]raft.ServerAddress, 3)

	for i, id := range nodeIDs {
		_, trans := raft.NewInmemTransport(raft.ServerAddress(id))
		transports[i] = trans
		addrs[i] = trans.LocalAddr()
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i != j {
				transports[i].Connect(addrs[j], transports[j])
			}
		}
	}

	nodes := make([]*consensus.RaftNode, 3)
	for i, id := range nodeIDs {
		node, err := consensus.NewInMemRaftNode(models.NodeID(id), "gaming", transports[i])
		check(err, "create raft node "+id)
		nodes[i] = node
	}
	defer func() {
		for _, n := range nodes {
			n.Shutdown()
		}
	}()

	servers := make([]raft.Server, 3)
	for i, id := range nodeIDs {
		servers[i] = raft.Server{
			ID:      raft.ServerID(id),
			Address: addrs[i],
		}
	}
	err := nodes[0].Bootstrap(servers)
	check(err, "bootstrap cluster")
	info("Cluster", "bootstrapped 3-node cluster from node-A")

	info("Election", "waiting for leader election...")
	var leader *consensus.RaftNode
	deadline := time.After(5 * time.Second)
	for leader == nil {
		select {
		case <-deadline:
			fmt.Println("  [ERROR] Leader election timed out")
			os.Exit(1)
		default:
			for _, n := range nodes {
				if n.IsLeader() {
					leader = n
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	info("Leader elected", "node=%s", leader.NodeID())

	for _, n := range nodes {
		info(string(n.NodeID()), "state=%s", n.State())
	}

	actions := []models.ModerationAction{
		{
			ID:          "mod-001",
			CommunityID: "gaming",
			ModeratorID: "admin",
			ActionType:  models.ModRemovePost,
			TargetHash:  "abc123",
			Reason:      "spam content",
			Timestamp:   time.Now(),
		},
		{
			ID:          "mod-002",
			CommunityID: "gaming",
			ModeratorID: "admin",
			ActionType:  models.ModBanUser,
			TargetUser:  "spammer42",
			Reason:      "repeated violations",
			Timestamp:   time.Now(),
		},
		{
			ID:          "mod-003",
			CommunityID: "gaming",
			ModeratorID: "admin",
			ActionType:  models.ModUnbanUser,
			TargetUser:  "spammer42",
			Reason:      "appeal accepted",
			Timestamp:   time.Now(),
		},
	}

	for _, a := range actions {
		err := leader.Propose(a)
		check(err, "propose "+a.ID)
		info("Committed", "[%s] %s target=%s reason=%q",
			a.ID, a.ActionType, target(a), a.Reason)
	}

	time.Sleep(200 * time.Millisecond)

	fmt.Println()
	for _, n := range nodes {
		modLog := n.GetLog()
		info(string(n.NodeID())+" log", "%d entries", len(modLog))
		for i, entry := range modLog {
			fmt.Printf("      %d. [index=%d] %s — %s %s\n",
				i+1, entry.LogIndex, entry.ActionType, target(entry), entry.Reason)
		}
	}

	fmt.Println()
}

func demoNetwork() {
	section("4. Gossip Network — Peer Discovery & CRDT Sync")
	fmt.Println()

	// Create three nodes with their own content stores
	store1, _ := storage.NewContentStore("")
	store2, _ := storage.NewContentStore("")
	store3, _ := storage.NewContentStore("")

	node1, err := network.NewGossipNode(network.GossipConfig{
		NodeID:   "gossip-node-1",
		BindAddr: "127.0.0.1",
		BindPort: 18001,
	}, store1)
	check(err, "create gossip node 1")
	defer node1.Shutdown()

	node2, err := network.NewGossipNode(network.GossipConfig{
		NodeID:   "gossip-node-2",
		BindAddr: "127.0.0.1",
		BindPort: 18002,
	}, store2)
	check(err, "create gossip node 2")
	defer node2.Shutdown()

	node3, err := network.NewGossipNode(network.GossipConfig{
		NodeID:   "gossip-node-3",
		BindAddr: "127.0.0.1",
		BindPort: 18003,
	}, store3)
	check(err, "create gossip node 3")
	defer node3.Shutdown()

	// Form cluster
	_, err = node2.Join([]string{"127.0.0.1:18001"})
	check(err, "node2 join")
	_, err = node3.Join([]string{"127.0.0.1:18001"})
	check(err, "node3 join")
	time.Sleep(100 * time.Millisecond)

	info("Cluster", "%d nodes connected via gossip", node1.NumMembers())

	// Create post on node1 and broadcast
	post := &models.Post{
		CommunityID: "distributed-systems",
		AuthorID:    "distributed-dev",
		Title:       "Hello from Node 1!",
		Body:        "This post will replicate to all nodes via gossip protocol.",
		CreatedAt:   time.Now(),
	}
	hash, _ := store1.StorePost(post)
	err = node1.BroadcastPost(post)
	check(err, "broadcast post")

	info("Node 1", "created and broadcast post %s", truncHash(hash))

	// Wait for propagation
	time.Sleep(200 * time.Millisecond)

	// Verify all nodes received the post
	posts1 := store1.GetAllPosts()
	posts2 := store2.GetAllPosts()
	posts3 := store3.GetAllPosts()

	info("Node 1 posts", "%d", len(posts1))
	info("Node 2 posts", "%d (received via gossip)", len(posts2))
	info("Node 3 posts", "%d (received via gossip)", len(posts3))

	// Demonstrate vote state CRDT sync
	subsection("Vote State Sync via CRDT")

	// Vote on post from different nodes
	vote1 := models.Vote{TargetHash: hash, UserID: "user-at-node1", Value: models.Upvote, Timestamp: time.Now()}
	store1.ApplyVote(vote1, "gossip-node-1")

	vote2 := models.Vote{TargetHash: hash, UserID: "user-at-node2", Value: models.Upvote, Timestamp: time.Now()}
	store2.StorePost(post) // Ensure post exists on node2 for voting
	store2.ApplyVote(vote2, "gossip-node-2")

	// Broadcast vote states
	vs1 := store1.GetVoteState(hash)
	vs2 := store2.GetVoteState(hash)
	node1.BroadcastVoteState(vs1)
	node2.BroadcastVoteState(vs2)

	time.Sleep(200 * time.Millisecond)

	// Check scores after CRDT merge
	score1, _ := store1.GetVoteScore(hash)
	score2, _ := store2.GetVoteScore(hash)

	info("Node 1 score", "%d (after CRDT merge)", score1)
	info("Node 2 score", "%d (after CRDT merge)", score2)

	fmt.Println()
}

func section(title string) {
	fmt.Printf("  %s\n", title)
}

func subsection(title string) {
	fmt.Printf("\n  ▸ %s\n", title)
}

func info(label, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("    %-30s %s\n", label+":", msg)
}

func check(err error, context string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [FATAL] %s: %v\n", context, err)
		os.Exit(1)
	}
}

func truncHash(h models.ContentHash) string {
	s := string(h)
	if len(s) > 12 {
		return s[:12] + "..."
	}
	return s
}

func target(a models.ModerationAction) string {
	if a.TargetUser != "" {
		return "user=" + string(a.TargetUser)
	}
	if a.TargetHash != "" {
		s := string(a.TargetHash)
		if len(s) > 12 {
			return "hash=" + s[:12] + "..."
		}
		return "hash=" + s
	}
	return ""
}

func init() {
	_ = strings.NewReader("")
}
