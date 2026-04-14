# Distributed-Reddit

A decentralized Reddit platform built in Go. Based on the DReddit paper by Saiteja Poluka, Shanmukha Vamshi Kuruba, and Jayanth Vunnam (CU Boulder).

The idea is simple: Reddit but without central servers. Posts, comments, and votes replicate across nodes using CRDTs (no coordination needed), while moderation actions go through Raft consensus so every node agrees on the same rules.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   DReddit Node                  │
├──────────────────┬──────────────────────────────┤
│   Discussion     │        Moderation            │
│   (Eventual)     │        (Strong)              │
│                  │                              │
│  Posts ──► GSet  │  Ban User ──► Raft Log       │
│  Votes ──► CRDT  │  Remove Post ──► Raft Log    │
│  Comments ──►    │  Unban User ──► Raft Log     │
│    Content Store │    Consensus (HashiCorp Raft)│
├──────────────────┴──────────────────────────────┤
│              Content-Addressed Storage          │
│           (SHA-256 hashing, JSON persistence)   │
└─────────────────────────────────────────────────┘
```

Two consistency models:
- **Eventual consistency** (CRDTs) for high-volume stuff like posts, comments, votes — nodes can update independently and merge later without conflicts
- **Strong consistency** (Raft) for moderation — all nodes must agree on the same ordered log of actions (ban user, remove post, etc.)

## What's implemented

### Core Models (`internal/models`)
All the data types: `Post`, `Comment`, `Vote`, `Community`, `ModerationAction`, `NodeInfo`, etc. Posts and comments have `ComputeHash()` for content-addressed storage.

### CRDTs (`internal/crdt`)
Four conflict-free replicated data types:

- **PNCounter** — positive-negative counter for vote scores. Each node tracks its own increments/decrements, merge takes the max per node
- **GSet** — grow-only set. Good for things that never get deleted (user registrations, post hashes). Merge = union
- **ORSet** — observed-remove set. Supports add AND remove. Concurrent add vs remove? Add wins (the unseen tag survives). Used for tracking community membership
- **LWWRegister** — last-writer-wins register. Two nodes write different values → the later timestamp wins. Used inside VoteState so each user's latest vote is their real vote
- **VoteState** — combines PNCounter + per-user LWWRegister for Reddit-style voting. Handles vote changes (upvote → downvote) correctly across nodes

### Content-Addressed Storage (`internal/storage`)
In-memory store with optional JSON disk persistence. Posts and comments are stored by their SHA-256 hash — same content always gets the same hash. Includes:
- Store/retrieve posts and comments by hash
- Community → posts index
- Post → comments index
- Vote tracking with CRDT merge support

### Raft Consensus (`internal/consensus`)
Wraps [HashiCorp Raft](https://github.com/hashicorp/raft) for moderation log replication. Each community gets its own Raft group (3-5 nodes). Includes:
- `ModerationFSM` — the state machine (implements `raft.FSM`). Applies committed moderation actions in order
- `RaftNode` — wrapper with `Propose()`, `GetLog()`, `AddVoter()`, `RemoveServer()`, cluster bootstrap
- Snapshot/restore support for log compaction
- Both TCP transport (production) and in-memory transport (testing)

### Gossip Network (`internal/network`)
HashiCorp memberlist-based gossip protocol for peer discovery and CRDT state synchronization:
- `GossipNode` — manages cluster membership and message broadcasting
- Peer discovery via SWIM protocol — nodes automatically discover each other
- Post/comment broadcasting — new content replicates to all peers
- VoteState sync — CRDT-based vote states merge across nodes without conflicts
- State sync requests — new nodes can request full state from existing peers
- Event callbacks — hooks for peer join/leave events

### DHT (`internal/dht`)
Consistent hash ring for mapping communities to responsible nodes:
- **CommunityDHT** — consistent hashing with configurable virtual nodes (default 150) for even distribution
- Configurable replication factor (default 3) — each community stored on N nodes
- Clockwise ring walk to find N distinct physical nodes for replication
- Explicit assignment overrides — pin specific communities to specific nodes
- Minimal disruption on node join/leave — consistent hashing means only ~1/N keys move
- `LookupNodes()`, `GetPrimaryNode()`, `IsResponsible()`, `GetDistribution()` for routing decisions
- Thread-safe with `sync.RWMutex`

### Demo (`cmd/demo`)
A runnable program that exercises everything end-to-end:
- Creates posts and comments across multiple communities
- Demonstrates CRDT merge behavior (counters, sets, voting)
- Spins up a 3-node Raft cluster, elects a leader, proposes moderation actions, and shows all nodes have the identical log
- Forms a 3-node gossip cluster, broadcasts posts, and shows CRDT vote sync
- Sets up a 5-node DHT, shows community routing, load distribution, explicit assignment overrides, minimal disruption on node join/leave, and DHT-routed storage

## What's pending

| Step | Package | Description |
|------|---------|-------------|
| 7 | `internal/community` | Community manager: ties storage + CRDTs + Raft together per community |
| 8 | `internal/node` | Node orchestrator: manages all communities on a single node, handles joins/leaves |
| 9 | `internal/api` | HTTP REST API: endpoints for creating posts, voting, moderating, etc. |
| 10 | `cmd/dreddit` | CLI + main binary |
| 11 | `ui/` | Web UI — browse communities, create posts, vote, and manage moderation from a browser |

Steps 7-8 wire everything together. Steps 9-10 give it a usable interface. Step 11 adds a proper web frontend on top of the HTTP API.

## Running

```bash
# run the demo
go run ./cmd/demo/ 2>/dev/null

# build
go build ./...
```

The `2>/dev/null` suppresses HashiCorp Raft and memberlist internal debug logs.

## Project Structure

```
Distributed-Reddit/
├── cmd/
│   └── demo/
│       └── main.go              # runnable demo
├── internal/
│   ├── models/
│   │   └── models.go            # core data types
│   ├── crdt/
│   │   └── crdt.go              # CRDT implementations
│   ├── storage/
│   │   └── content_store.go     # content-addressed storage
│   ├── consensus/
│   │   ├── fsm.go               # Raft finite state machine
│   │   └── raft.go              # HashiCorp Raft wrapper
│   ├── network/
│   │   ├── gossip.go            # Gossip protocol implementation
│   │   └── gossip_test.go       # 14 tests
│   └── dht/
│       ├── dht.go               # Consistent hash ring DHT
│       └── dht_test.go          # 37 tests
├── tests/
│   └── integration_test.go      # 8 cross-layer integration tests
├── go.mod
├── go.sum
└── LICENSE
```

## Tech Stack
- **Go** — all backend code
- **HashiCorp Raft** — consensus protocol for moderation
- **HashiCorp memberlist** — gossip protocol for peer discovery and CRDT sync
- **Consistent Hashing** — community-to-node mapping with virtual nodes
- **SHA-256** — content-addressed hashing
- **JSON** — serialization and disk persistence
- **Web UI** — frontend (TBD)

## References
- DReddit: A Decentralized Reddit — Saiteja Poluka, Shanmukha Vamshi Kuruba, Jayanth Vunnam (University of Colorado Boulder)
- [HashiCorp Raft](https://github.com/hashicorp/raft)
- [CRDTs: Consistency without consensus](https://crdt.tech/)
