# Distributed-Reddit (DReddit)

A fully decentralized Reddit platform built in Go. Based on the DReddit paper by Saiteja Poluka, Shanmukha Vamshi Kuruba, and Jayanth Vunnam (CU Boulder).

The idea is simple: Reddit but without central servers. Posts, comments, and votes replicate across nodes using CRDTs (no coordination needed), while moderation actions go through Raft consensus so every node agrees on the same rules.

## Architecture

```text
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

- **Eventual consistency** (CRDTs) for high-volume operations like posts, comments, and votes. Nodes can update independently and merge later without conflicts.
- **Strong consistency** (Raft) for moderation. All nodes must agree on the same ordered log of actions (ban user, remove post, etc.).

## What's Implemented

### Core Models & CRDTs (`internal/models`, `internal/crdt`)

Four conflict-free replicated data types ensuring mathematical consistency across the network:

- **PNCounter** — Positive-negative counter for vote scores.
- **GSet** — Grow-only set for appending content.
- **ORSet** — Observed-remove set tracking community membership.
- **LWWRegister** — Last-writer-wins register to handle individual users changing their votes.
- **VoteState** — Combines PNCounter + per-user LWWRegister for Reddit-style voting.

### Content-Addressed Storage & Persistence (`internal/storage`)

In-memory store with automatic JSON disk persistence. Posts, comments, and CRDT vote states are continually saved to the disk and instantly reloaded into memory when the server boots.

### Raft Consensus (`internal/consensus`)

Wraps [HashiCorp Raft](https://github.com/hashicorp/raft) for moderation log replication. Each community gets its own Raft group ensuring strong consistency for admin actions.

### Gossip Network & DHT (`internal/network`, `internal/dht`)

- **SWIM Protocol:** HashiCorp memberlist-based gossip protocol for peer discovery, post/comment broadcasting, and CRDT vote synchronization.
- **Consistent Hashing:** DHT ring with configurable virtual nodes to accurately route and distribute communities across available physical nodes.

### Node & Community Orchestration (`internal/node`, `internal/community`)

- **Community Manager:** Binds storage, CRDTs, and Raft consensus together for individual topic isolation.
- **Node Orchestrator:** Manages physical node lifecycles, auto-loads saved community data from disk upon startup, and handles DHT announcements.

### REST API & Authentication (`internal/api`)

A comprehensive HTTP REST API serving endpoints for reading/writing posts, comments, and voting. Includes cryptographic user authentication (SHA-256) with signup/login endpoints protecting network interactions.

### Decentralized Web UI (`ui/`)

A fully responsive, JavaScript-driven frontend acting as a true Reddit clone. Features include live continuous polling, persistent comment tracking, auto-refresh pauses while typing, and a secure authentication modal overlay.

---

## Setup Instructions

### Prerequisites

- Go 1.21 or higher
- A modern web browser

### 1. Clone the Repository

```bash
git clone [https://github.com/Shan-Vision05/Distributed-Reddit.git](https://github.com/Shan-Vision05/Distributed-Reddit.git)
cd Distributed-Reddit
```

### 2. Download Dependencies

```bash
go mod tidy
```

### 3. Run the Application

Start your primary DReddit node on port 8080. This boots the API, initializes the Gossip and DHT networks, and serves the Web UI.

```bash
go run cmd/dreddit/main.go -id node1 -addr :8080
```

Open your browser and navigate to `http://localhost:8080`.

_(Note: Ensure your `.gitignore` is configured to ignore local `data\__`and`raft*` directories so your local testing data is not pushed to version control.)*

---

## Testing Procedures

To verify the distributed architecture and frontend functionality, follow these end-to-end testing steps to assist in running the application:

### Test 1: Account Creation & Authentication

1. Navigate to `http://localhost:8080`.
2. Click **Log In** in the top right. Switch the modal to **Sign Up**.
3. Create a new user with a username and password.
4. **Expected Result:** The UI grants access to the platform, replaces the login button with your username, and your hashed credentials are saved locally.

### Test 2: Community Initialization & Persistence

1. In the sidebar, type `golang` into the Join input and submit.
2. **Expected Result:** The DHT announces the new community in the terminal. The UI switches to `d/golang`.
3. Create a post and shut down the Go server (`Ctrl + C`).
4. Boot the server back up and refresh the browser.
5. **Expected Result:** The node orchestrator automatically scans the disk, rejoins `golang`, and perfectly restores your post and its CRDT vote state.

### Test 3: Multi-Node Gossip Replication (P2P Simulation)

1. Open a second terminal window and run a second peer node on a different port:
   ```bash
   go run cmd/dreddit/main.go -id node2 -addr :8081
   ```
2. Using cURL, query the API of `node2` for the posts in `d/golang`:
   ```bash
   curl -X GET "http://localhost:8081/api/posts?community_id=golang"
   ```
3. **Expected Result:** The newly spun-up node immediately returns the posts created on `node1`, proving the Gossip network successfully synchronized the decentralized application state.

---

## Project Structure

```text
Distributed-Reddit/
├── cmd/
│   ├── demo/                    # Headless backend demo
│   └── dreddit/                 # Main application entry point
├── internal/
│   ├── api/                     # HTTP REST endpoints & Auth
│   ├── community/               # Per-community orchestration
│   ├── consensus/               # Raft finite state machine
│   ├── crdt/                    # CRDT mathematical logic
│   ├── dht/                     # Consistent hash ring routing
│   ├── models/                  # Core data structures
│   ├── network/                 # Memberlist Gossip protocol
│   ├── node/                    # Physical node management
│   └── storage/                 # Persistence & JSON caching
├── tests/                       # Cross-layer integration tests
├── ui/
│   └── index.html               # Vanilla JS/HTML/CSS Frontend
├── go.mod
├── go.sum
└── LICENSE
```

## Tech Stack

- **Go** — Core backend engine
- **HashiCorp Raft** — Consensus protocol for moderation
- **HashiCorp Memberlist** — Gossip protocol for peer discovery and CRDT sync
- **Consistent Hashing** — Community-to-node mapping
- **Vanilla Web (HTML/CSS/JS)** — Lightweight decoupled frontend

## References

- DReddit: A Decentralized Reddit — Saiteja Poluka, Shanmukha Vamshi Kuruba, Jayanth Vunnam (University of Colorado Boulder)
- [HashiCorp Raft](https://github.com/hashicorp/raft)
- [CRDTs: Consistency without consensus](https://crdt.tech/)
