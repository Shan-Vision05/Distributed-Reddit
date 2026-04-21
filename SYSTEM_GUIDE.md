# Distributed-Reddit Comprehensive System Guide

This document is a code-based guide to the current Distributed-Reddit repository. It is written from the implementation that exists in this project, not from an idealized architecture. Where the code, tests, and earlier docs diverge, this guide reflects the code and calls out the gap.

The project is a decentralized Reddit-style system written in Go. It combines several distributed systems techniques in one codebase:

- Gossip-based peer discovery and state dissemination
- CRDT-based eventual consistency for votes
- Raft-based strong consistency for moderation actions
- Content-addressed storage for posts and comments
- Per-node persistence for content, moderation state, and authentication
- A single-page demo UI that can switch across replicas

The codebase is organized to demonstrate the difference between eventually consistent collaborative data and strongly consistent administrative actions.

## 1. Project Purpose

At a high level, this system is trying to answer this question:

How can a Reddit-like application work without a single central server?

The answer implemented here is:

- let content propagate through a peer-to-peer gossip network
- let votes converge through CRDT merges
- let moderation go through Raft so every participating replica agrees on the same moderation log
- store data locally on each node so a node can survive restarts

This means the system is intentionally hybrid. It is not purely eventually consistent and it is not purely consensus-driven. It uses different consistency models for different problems.

## 2. What a Node or Replica Means Here

In this repository, a node and a replica are effectively the same thing: one running process of the `dreddit` binary.

If you run the demo cluster, you start multiple separate processes, each with:

- its own HTTP API server
- its own gossip listener
- its own local data directory
- its own local user database
- its own in-memory content store
- its own per-community Raft instances

For the five-node demo, the processes are typically:

- `demo-node1` at `http://localhost:8091`
- `demo-node2` at `http://localhost:8092`
- `demo-node3` at `http://localhost:8093`
- `demo-node4` at `http://localhost:8094`
- `demo-node5` at `http://localhost:8095`

They are called replicas because they replicate content and vote state across one another.

## 3. Core Consistency Model

The project uses two different consistency models.

### 3.1 Eventual Consistency

The following are eventually consistent:

- posts
- comments
- votes

Posts and comments are broadcast through gossip. Votes are merged using CRDT rules. A node may see slightly stale data for a short period, but the system is designed so replicas converge.

### 3.2 Strong Consistency

The following go through Raft consensus:

- remove post
- restore post
- remove comment
- ban user
- unban user

These actions are written as ordered moderation log entries in a per-community Raft group. The application then uses that moderation log during reads to filter what should be visible and which users should be blocked.

## 4. Repository Overview

The repository structure is centered around runtime packages in `internal/`, entry points in `cmd/`, shell scripts in `scripts/`, tests in `tests/`, and a single HTML UI in `ui/`.

### 4.1 Entry Points

`cmd/dreddit/main.go`

- parses command-line flags
- constructs a `node.Node`
- constructs an `api.Server`
- starts the HTTP server

Main flags:

- `-id`: node identity
- `-addr`: HTTP bind address
- `-gossip-port`: gossip/memberlist port
- `-peers`: seed gossip peers
- `-data-dir`: local persistence directory

`cmd/demo/main.go`

- runs an interactive terminal demonstration of individual subsystems
- showcases storage, CRDTs, Raft, gossip, and DHT behavior without being the main application path

### 4.2 Internal Packages

`internal/models`

- foundational data types such as `Post`, `Comment`, `Vote`, `ModerationAction`, `Community`, and `NodeInfo`

`internal/crdt`

- conflict-free replicated data types including `PNCounter`, `GSet`, `ORSet`, `LWWRegister`, and `VoteState`

`internal/storage`

- thread-safe in-memory content store with JSON persistence on disk

`internal/network`

- gossip network using HashiCorp memberlist for discovery and message dissemination

`internal/consensus`

- Raft setup and FSM for moderation actions

`internal/dht`

- consistent-hashing ring and explicit community assignment tracking

`internal/community`

- per-community manager that connects storage, gossip, and Raft

`internal/node`

- top-level node orchestration, startup, joining communities, managing Raft instances, gossip callbacks, and DHT updates

`internal/api`

- HTTP endpoints, auth handling, moderation routing, and status reporting

### 4.3 Frontend

`ui/index.html`

- the entire UI is a single static HTML file with embedded CSS and JavaScript
- it talks directly to the HTTP API
- it supports multi-replica demos from one browser page

### 4.4 Scripts

`scripts/run_demo_cluster.sh`

- builds the application
- starts a five-node local cluster with separate data directories

`scripts/test_cluster.sh`

- runs a scripted multi-node verification flow focused on the core bugs and cluster behavior

`scripts/test_cluster_full.sh`

- runs a broader five-node verification flow covering content replication, moderation, community isolation, and restart behavior

### 4.5 Tests

There are package-level tests and repository-level integration tests. Together they verify many of the behaviors demonstrated in the UI and scripts.

## 5. Data Model

### 5.1 Common Types

The core identity types are aliases of strings:

- `NodeID`
- `CommunityID`
- `UserID`
- `ContentHash`

This keeps serialized messages simple and explicit.

### 5.2 Post

Important fields:

- `Hash`
- `CommunityID`
- `AuthorID`
- `Title`
- `Body`
- `URL`
- `CreatedAt`
- `IsRemoved`

The `ComputeHash()` method hashes the post’s content using SHA-256 over a serialized subset of fields. The hash is deterministic for the same content and timestamp.

Consequence:

- identical content with identical timestamps hashes to the same ID
- posts are effectively immutable content objects
- editing a post is not implemented and would not fit the current storage model naturally

### 5.3 Comment

Important fields:

- `Hash`
- `PostHash`
- `ParentHash`
- `AuthorID`
- `Body`
- `CreatedAt`
- `IsRemoved`

Comments can represent threaded replies because `ParentHash` is available, but the current UI does not render a deep tree structure. It renders a flat list per post.

### 5.4 Vote

Important fields:

- `TargetHash`
- `UserID`
- `Value`
- `Timestamp`

Votes target either a post or a comment by hash.

### 5.5 ModerationAction

Important fields:

- `CommunityID`
- `ModeratorID`
- `ActionType`
- `TargetHash`
- `TargetUser`
- `Reason`
- `Timestamp`
- `LogIndex`

These actions are what Raft replicates.

## 6. Content-Addressed Storage

The content store lives in `internal/storage/content_store.go`.

It is a shared store for all communities hosted on one node.

Internally it maintains:

- `posts`: map from hash to post
- `comments`: map from hash to comment
- `communityPosts`: map from community to ordered list of post hashes
- `postComments`: map from post hash to ordered list of comment hashes
- `votes`: map from content hash to `VoteState`

### 6.1 StorePost Flow

When a post is stored:

1. the store computes the content hash
2. the post is inserted into `posts`
3. the post hash is appended to `communityPosts[communityID]`
4. a `VoteState` is created if one does not already exist
5. the post is persisted to disk if a data directory exists

### 6.2 StoreComment Flow

When a comment is stored:

1. the store computes the content hash
2. the comment is inserted into `comments`
3. the hash is appended to `postComments[postHash]`
4. a vote state is initialized for the comment if needed
5. the comment is persisted to disk

### 6.3 Vote Storage

The store does not store vote events as an append-only log. It stores the CRDT state for each content hash.

When a vote is applied:

1. it finds the `VoteState` for the target
2. the vote state applies the change relative to the user’s previous vote
3. the new vote state is persisted to disk

### 6.4 Disk Layout

When a node is started with a data directory, the content store uses a `store/` subdirectory.

Layout:

```text
<data-dir>/
  users.json
  store/
    posts/
      <hash>.json
    comments/
      <hash>.json
    votes/
      <hash>.json
  raft_<community>/
    raft/
      raft.db
      snapshots/
```

### 6.5 LoadFromDisk

On startup the content store reads:

- all posts from `store/posts`
- all comments from `store/comments`
- all vote states from `store/votes`

It rebuilds the in-memory indexes (`communityPosts`, `postComments`) as it loads.

## 7. CRDT Layer

The CRDT package exists to let replicas converge without a central coordinator.

### 7.1 PNCounter

`PNCounter` maintains:

- a positive map keyed by node ID
- a negative map keyed by node ID

Each node increments its own bucket. Merge takes the max per node for positive and negative counts. The final value is the sum of positives minus the sum of negatives.

This works because each node’s contribution is monotonic.

### 7.2 GSet

`GSet` is a grow-only set.

Use case in this project:

- mostly educational and demonstrative rather than central to the runtime path

### 7.3 ORSet

`ORSet` is an observed-remove set with tombstones. It supports concurrent add and remove operations in a CRDT-safe way.

Again, it is present mainly as part of the project’s distributed-data toolkit and demonstration layer.

### 7.4 LWWRegister

`LWWRegister` stores:

- a value
- a timestamp
- a node ID

During merge:

- newer timestamps win
- if timestamps are equal, lexicographically larger node ID wins

### 7.5 VoteState

`VoteState` combines:

- one `PNCounter` as the aggregate score
- one `LWWRegister` per user to remember that user’s latest vote

This design enforces the Reddit rule that a user’s most recent vote supersedes their previous one.

Apply flow:

1. if the user already voted, undo the old contribution from the counter
2. apply the new contribution
3. update that user’s register

Merge flow:

1. merge the `PNCounter`
2. merge each user register using last-write-wins semantics

Important implication:

The score converges, but it depends on the assumptions of the CRDT merge model. The implementation is suited for a demo and the current tests validate convergence in typical multi-node cases.

## 8. Gossip Network

The gossip implementation is in `internal/network/gossip.go` and is built on HashiCorp memberlist.

### 8.1 What Gossip Handles

The gossip layer handles:

- peer discovery
- membership change callbacks
- post broadcast
- comment broadcast
- vote state broadcast
- state sync request and response
- community announce messages

### 8.2 Message Types

The defined message types are:

- `MsgTypePost`
- `MsgTypeComment`
- `MsgTypeVote`
- `MsgTypeStateSyncRequest`
- `MsgTypeStateSyncResponse`
- `MsgTypeCommunityAnnounce`

Every message is wrapped in a `GossipMessage` containing:

- type
- sender ID
- timestamp
- raw JSON payload

### 8.3 Broadcast Strategy

The implementation uses both:

- memberlist’s broadcast queue
- direct reliable send to peers

That improves the chance of quick propagation in the demo environment.

### 8.4 State Sync

When a new peer joins:

1. the event delegate notices the join
2. the node sends state sync to the new member
3. the node also requests state sync from the new member

State sync includes:

- all posts
- all comments
- all vote states

This means newly joined nodes can catch up quickly even if they missed earlier broadcasts.

### 8.5 Idempotence

For posts and comments, the receiver first checks whether it already has the content hash. Duplicate deliveries are ignored.

For votes, the incoming vote state is merged.

## 9. DHT and Community Distribution

The DHT implementation is in `internal/dht/dht.go`.

It is a consistent-hash ring with configurable virtual nodes and replication factor.

### 9.1 What It Does Well Right Now

- tracks known physical nodes
- can look up which nodes should be responsible for a community according to the ring
- supports explicit community assignments that override hash-based lookup
- exposes helper methods for distribution analysis

### 9.2 What It Is Actually Used For in This Codebase

In the current runtime implementation, the DHT is used more as:

- an assignment-tracking structure
- a cluster metadata structure
- a routing aid for demo logic

It is not used to transparently route all application requests to responsible nodes. A request still hits whichever HTTP node the client selected.

### 9.3 Explicit Assignment via Community Announce

When a node announces it is hosting a community, the receiving node calls `AssignCommunity` with that announcer node.

That means explicit assignment can override ring-based selection and reflect actual host knowledge learned from the gossip plane.

### 9.4 Important Limitation

The system does not currently provide a global community directory API backed by the DHT. The UI’s communities list is driven by the node’s locally joined communities, not every community known anywhere in the cluster.

## 10. Raft Consensus Layer

The consensus layer is in `internal/consensus/raft.go` and `internal/consensus/fsm.go`.

### 10.1 Per-Community Raft

Every joined community gets its own Raft node on a given replica.

This is important:

- moderation is scoped to a community
- each community can have a distinct membership and leader
- moderation for one community is isolated from another

### 10.2 Raft Storage

If `DataDir` is provided:

- Raft uses BoltDB for log and stable storage
- file snapshots are stored on disk

If `DataDir` is empty:

- Raft uses in-memory stores only

### 10.3 ModerationFSM

The FSM is very simple. It stores an append-only slice of `ModerationAction` values.

When a log entry is applied:

1. the JSON payload is unmarshaled into `ModerationAction`
2. its `LogIndex` is filled from the Raft log index
3. it is appended to the FSM log

When reads happen, the API scans this moderation log to decide:

- which posts to hide
- which comments to hide
- which users are banned

### 10.4 This Means Moderation Is Evaluated at Read Time

The store does not physically remove posts or comments when moderation happens.

Instead:

- the raw content remains in the content store
- the API filters it out according to the moderation log

This is effectively soft-delete behavior driven by the moderation log.

## 11. Node Orchestration

The node package is the runtime heart of the backend.

### 11.1 What a Node Owns

A node owns:

- one `CommunityDHT`
- one `GossipNode`
- one shared `ContentStore`
- a map of community managers
- a map of Raft nodes per community
- a map of Raft bind addresses per community
- a map of `remoteCommunities`

### 11.2 Startup Sequence

When `NewNodeWithConfig` runs:

1. it creates the node’s data directory if requested
2. it creates the DHT and inserts itself as a node
3. it creates the shared content store
4. it creates the gossip node
5. it installs gossip callbacks
6. it joins the gossip cluster if peers were supplied
7. it auto-rejoins communities by scanning `raft_*` directories in the data directory

### 11.3 Community Join Decision

The API calls one of two methods:

- `JoinCommunity`
- `JoinCommunityAsFollower`

The choice depends on whether `HasRemoteCommunity(communityID)` is true.

If no remote node has announced the community yet:

- this node bootstraps a Raft cluster for that community

If another node already announced it:

- this node starts as a follower and waits to be added by the leader

This is how the code avoids the obvious split-brain case of every node bootstrapping a separate single-node Raft cluster for the same community.

### 11.4 Peer Join Callback

When a new peer joins the gossip network:

- the DHT adds that node
- this node re-broadcasts all of its known community announcements

That helps late joiners learn which communities already exist and should be joined in follower mode.

### 11.5 Community Announce Callback

When a peer announces community membership:

1. DHT assignment is updated
2. `remoteCommunities[communityID]` is marked if the announcer is another node
3. if this node already hosts the community and is currently Raft leader, it adds the announcing node as a voter

## 12. Community Manager

The community manager is a small but important integration layer.

It owns references to:

- the community ID
- the local node ID
- the shared content store
- the gossip node
- the community’s Raft node

It is responsible for turning high-level community operations into the correct lower-level operations.

### 12.1 CreatePost

- validates that the post belongs to this manager’s community
- stores it locally
- broadcasts it through gossip

### 12.2 CreateComment

- stores comment locally
- broadcasts comment through gossip

### 12.3 Vote

- applies the vote to the shared content store
- fetches the latest vote state
- broadcasts that vote state

### 12.4 Moderate

- validates community ownership
- proposes moderation action to Raft

## 13. Authentication Model

Authentication is implemented in `internal/api/api.go`.

### 13.1 What Exists

Each API server has:

- `users`: map from username to hashed password
- `tokens`: map from token to username
- `authFile`: JSON file path, usually `<data-dir>/users.json`

### 13.2 Signup

Signup flow:

1. `POST /api/signup`
2. request body includes username and password
3. if the username already exists locally on that node, return `409 Conflict`
4. hash password with SHA-256
5. generate a cryptographically random token
6. store user and token in memory
7. persist to `users.json`
8. return token and user ID

### 13.3 Login

Login flow:

1. `POST /api/login`
2. find local user on that same node
3. compare SHA-256 hash of supplied password
4. generate new token
5. persist token map
6. return token and user ID

### 13.4 Important Reality: Auth Is Per Node

User accounts are not replicated across replicas.

Consequences:

- signing up on node1 does not create the same account on node2
- a login session on one replica does not automatically work on another unless that token exists in that node’s own `users.json`
- the UI therefore stores sessions per API endpoint

This is one of the biggest implementation differences between this project and a production Reddit-like user experience.

### 13.5 Security Note

Passwords are hashed with plain SHA-256, not a password-hardening algorithm such as bcrypt, scrypt, or Argon2. That is acceptable for a learning demo but not for real-world account security.

## 14. HTTP API

The backend serves the static UI at `/` and API endpoints under `/api`.

### 14.1 `POST /api/signup`

Request:

```json
{
  "username": "alice",
  "password": "pw1"
}
```

Response:

```json
{
  "token": "...",
  "user_id": "alice"
}
```

### 14.2 `POST /api/login`

Same request shape as signup.

Returns a fresh token.

### 14.3 `GET /api/status`

No auth required.

Returns data such as:

- node ID
- local gossip address and port
- gossip peer addresses
- member count
- locally joined communities

This endpoint is heavily used by the UI and by cluster scripts.

### 14.4 `GET /api/communities`

Returns the list of communities joined on that local node.

It does not return all communities that may exist elsewhere in the cluster.

### 14.5 `POST /api/join`

Requires bearer token.

Request:

```json
{
  "community_id": "golang"
}
```

Behavior:

- if already locally joined, returns success idempotently unless the user is banned in that community
- if a remote node has already announced this community, this node joins as a Raft follower
- otherwise it bootstraps a new Raft cluster for that community

### 14.6 `GET /api/posts`

Query parameter:

- `community_id`

Returns posts with their scores.

The handler reads the moderation log and filters out:

- removed posts
- posts by banned users

### 14.7 `POST /api/post`

Requires bearer token.

Request shape:

```json
{
  "community_id": "golang",
  "title": "Go Consensus",
  "body": "Raft details"
}
```

Behavior:

- user ID is taken from the validated token, not the client body
- if user is banned in that community, request is rejected
- the server sets timestamp and hash
- the community manager stores and gossips the post

### 14.8 `GET /api/comments`

Query parameters:

- `community_id`
- `post_hash`

Returns comments with their scores.

The handler filters out:

- removed comments
- comments by banned users

### 14.9 `POST /api/comment`

Requires bearer token.

Request shape:

```json
{
  "community_id": "golang",
  "comment": {
    "post_hash": "<post-hash>",
    "body": "Great post"
  }
}
```

`parent_hash` can also be supplied for reply-style comments.

### 14.10 `POST /api/vote`

Requires bearer token.

Request shape:

```json
{
  "community_id": "golang",
  "vote": {
    "target_hash": "<content-hash>",
    "value": 1
  }
}
```

Use `1` for upvote and `-1` for downvote.

### 14.11 `POST /api/moderate`

Requires bearer token.

Supported UI action strings:

- `DELETE_POST`
- `RESTORE_POST`
- `DELETE_COMMENT`
- `BAN_USER`
- `UNBAN_USER`

These are mapped to model-level moderation action constants before submission to Raft.

Authorization rules in the handler:

- only username `admin` can ban or unban users
- non-admins can delete only their own posts

This authorization logic is intentionally simple and hard-coded for demo use.

## 15. Frontend and How to Use It

The UI is a single-page application implemented in `ui/index.html`.

### 15.1 Main UI Areas

- sticky header with active API and session status
- main feed area for posts and comments
- right-hand sidebar for replica control, post creation, moderation, and community join

### 15.2 Replica Awareness

The UI can track multiple API endpoints at once.

It supports:

- custom replica list
- one-click current-node preset
- five-node demo preset
- cluster probing via `/api/status`
- active replica summary card
- per-replica health and community list

This is one of the project’s best demo features because it makes distributed behavior visible from a single page.

### 15.3 Sessions Per Replica

The browser stores the active session per API endpoint in `localStorage`.

That means:

- switch from node1 to node2 and you may appear logged out there
- switch back to node1 and your previous node1 session is restored if that token was saved locally

This matches the backend’s per-node auth model.

### 15.4 Polling

The UI automatically polls:

- posts every 5 seconds
- status every 4 seconds

There is a pause/resume control. The UI also avoids reloading the feed while the user is focused in an input field to reduce disruption.

### 15.5 Post Workflow in UI

1. log in or sign up on the active replica
2. join a community on that replica
3. click `Create Post`
4. enter title and body
5. submit

The post appears locally and should propagate to other replicas that have the relevant community content.

### 15.6 Comment Workflow in UI

1. open comments for a post
2. type in the reply field
3. submit comment

Comments are fetched lazily when the user opens a comment section.

### 15.7 Voting Workflow in UI

Each post has upvote and downvote buttons.

Comment votes are supported by the backend and reflected in comment API responses, though the current UI focuses more strongly on post scores and basic comment display.

### 15.8 Moderation Workflow in UI

If the current user is `admin`, the admin console becomes visible.

The admin console lets you:

- pick an action
- fill a target hash or target username
- execute the action

There are also helper buttons on posts and comments to prefill targets.

## 16. End-to-End Operational Flow

This section describes what actually happens during common operations.

### 16.1 Creating a Community on a Fresh Cluster

Suppose node1 has no prior knowledge of `golang`.

Flow:

1. user sends `POST /api/join` with `community_id = golang`
2. API sees there is no local membership yet
3. API checks `HasRemoteCommunity(golang)` and gets false
4. node calls `JoinCommunity(golang)`
5. node creates a Raft instance for `golang` with `Bootstrap = true`
6. node creates a `community.Manager`
7. node broadcasts `CommunityAnnounce` for `golang`

At that point node1 is the initial Raft host for that community.

### 16.2 Joining the Same Community from Another Node

Suppose node2 has already learned through gossip that node1 hosts `golang`.

Flow:

1. user on node2 calls `POST /api/join` for `golang`
2. API checks `HasRemoteCommunity(golang)` and gets true
3. node2 runs `JoinCommunityAsFollower(golang)`
4. node2 starts a non-bootstrap Raft instance for `golang`
5. node2 broadcasts `CommunityAnnounce`
6. if node1 is leader for `golang`, node1 receives the announcement and adds node2 as a Raft voter

This forms a real multi-node moderation cluster for that community.

### 16.3 Creating a Post

1. authenticated user calls `POST /api/post`
2. API rewrites `AuthorID` from token identity
3. API ensures the node has joined the target community
4. API blocks the write if the user is banned
5. manager stores the post
6. store persists the post and initializes its vote state
7. manager broadcasts the post via gossip
8. peers receive and store the same post if they do not already have it

### 16.4 Voting on a Post

1. authenticated user sends a vote to the active replica
2. API sets the vote timestamp and user ID
3. manager applies vote locally in the CRDT vote state
4. manager broadcasts the resulting vote state
5. peers merge the incoming vote state
6. scores eventually converge

### 16.5 Deleting a Post

1. moderator submits `DELETE_POST`
2. API maps that to `ModRemovePost`
3. manager submits it to Raft
4. Raft replicates it to the community quorum
5. FSM appends it to the moderation log
6. subsequent `GET /api/posts` calls hide that post

The post still exists in raw storage, but it is filtered from the feed.

## 17. Scripts and How to Use Them

### 17.1 `scripts/run_demo_cluster.sh`

Purpose:

- start a local five-replica demo using persistent temp directories

Typical usage:

```bash
./scripts/run_demo_cluster.sh
```

What it does:

- builds `./dreddit`
- kills earlier demo-node processes
- starts five nodes with HTTP ports `8091` through `8095`
- assigns gossip ports `11101` through `11105`
- writes logs to `/tmp/dreddit_demo_cluster/node*.log`

### 17.2 `scripts/test_cluster.sh`

Purpose:

- exercise the main three-node cluster story and validate known bug fixes and core replication flows

### 17.3 `scripts/test_cluster_full.sh`

Purpose:

- run a broader five-node scenario that verifies auth, communities, post/comment replication, vote convergence, moderation, isolation, and restarts

This is the closest thing in the repo to an executable acceptance test for the demo architecture.

## 18. Tests and What They Prove

### 18.1 API Tests

`internal/api/api_test.go` verifies, among other things:

- signup returns token and user ID
- login returns token and user ID
- protected endpoints reject missing auth tokens
- moderation action mapping works correctly
- bans populate `TargetUser` correctly and are enforced

### 18.2 DHT Tests

`internal/dht/dht_test.go` verifies:

- default and custom DHT configuration
- virtual node ring size
- deterministic lookup behavior
- respecting replication factor
- cleanup when nodes are removed
- assignment management

### 18.3 Gossip Tests

`internal/network/gossip_test.go` verifies:

- gossip node creation
- cluster joining
- peer join callback behavior
- post broadcast replication
- comment broadcast replication
- vote state broadcast and merge
- idempotent post delivery

### 18.4 Repository Integration Tests

`tests/bugs_test.go` and `tests/integration_test.go` verify:

- community managers and gossip share the same store
- community membership announcements propagate
- follower-mode multi-node Raft formation works
- DHT routing and failover simulations
- vote convergence across replicas
- multi-community isolation

## 19. What the System Supports Today

Current implemented features include:

- running multiple replicas locally
- browsing the active replica through one UI
- creating accounts per replica
- logging in per replica
- creating or joining communities
- creating posts
- creating comments
- voting on posts and comments through the backend
- propagating posts and comments through gossip
- converging votes through CRDTs
- moderating through Raft-backed actions
- restart persistence for content and community Raft state
- cluster status probing and failover demos in the UI

## 20. What Is Different from Real Reddit

This system is Reddit-like, not Reddit-complete.

Important differences:

- no global user identity across replicas
- no global community directory API
- no subreddit discovery or search
- no post editing
- no rich comment tree UI
- no media handling pipeline
- no real permissions model beyond the hard-coded `admin` username and self-delete rule
- no strong password hashing or hardened auth service
- no background compaction of old content or moderation tombstones
- no recommendation, ranking, sorting variants, or subscriptions

## 21. Known Architectural Limitations

### 21.1 User Accounts Are Local to Each Node

This is the most visible limitation.

It affects:

- login portability
- identity consistency
- global username uniqueness

### 21.2 Communities Are Not Fully Discoverable Cluster-Wide Through the UI

The UI shows locally joined communities from the active node. It does not expose an authoritative cluster-wide directory.

### 21.3 Moderation Is Read-Time Filtering

Removed content is not purged from local stores. The API simply filters it out using the moderation log.

### 21.4 `admin` Is Hard-Coded as the Special Moderator Identity

There is no moderator role assignment workflow yet.

### 21.5 Security Is Demo Grade

- plain SHA-256 for passwords
- no TLS in the app layer
- no signed inter-node messages
- local bearer-token maps only

### 21.6 DHT Is Not the Request Router

The DHT helps represent responsibility and placement, but application traffic is not automatically redirected according to DHT ownership.

## 22. Suggested Roadmap if You Wanted to Extend It

The most natural next improvements are:

1. global community discovery and metadata replication
2. replicated or consensus-backed user identity
3. stronger auth and password hashing
4. proper moderator roles and community membership models
5. explicit replica placement and DHT-driven routing
6. richer UI for nested comment trees and moderation history
7. better failure handling and recovery tests

## 23. How to Use the Application Step by Step

### 23.1 Single Node

Run:

```bash
go run ./cmd/dreddit -id node1 -addr :8080 -data-dir ./data/node1
```

Then open:

```text
http://localhost:8080
```

Workflow:

1. sign up
2. join a community like `golang`
3. create a post
4. open comments and reply
5. upvote or downvote content
6. if signed in as `admin`, use moderation controls

### 23.2 Five-Node Demo

Run:

```bash
./scripts/run_demo_cluster.sh
```

Open:

```text
http://localhost:8091
```

Then:

1. click `Use 5-Node Demo`
2. click `Probe Cluster`
3. sign up on the active replica
4. join `golang`
5. create a post
6. switch to another replica card
7. sign up there if necessary
8. join the same community there
9. verify posts appear across nodes
10. cast votes from different replicas and watch the score converge
11. sign up as `admin` on a replica and test moderation actions

### 23.3 Failover Demo

With the cluster running:

1. keep the UI open on one replica
2. kill that replica’s process
3. the UI status should show it as down after probing
4. switch to another replica
5. continue reading and interacting with the replicated content

### 23.4 Restart Persistence Demo

1. create communities and content on a node with a data dir
2. stop the process
3. restart the same node with the same data dir
4. verify content, sessions, and community Raft state are still present

## 24. Questions People Can Ask About This System, With Detailed Answers

### Q1. Why are posts and comments not sent through Raft too?

Because the system is deliberately separating high-volume discussion traffic from control-plane moderation traffic.

If every post, comment, and vote had to go through Raft:

- latency would be higher
- the write path would be leader-centric
- availability would depend more heavily on quorum for ordinary user activity
- throughput would be lower

By using gossip and CRDTs for content interactions, the system allows replicas to accept and spread content with less coordination. Raft is reserved for the operations where ordering and agreement matter most: moderation.

### Q2. If a post is created on one replica, why can another replica eventually see it?

Because the post is stored locally and then broadcast as a gossip message. When another replica receives that post message, it checks whether it already has that hash. If not, it stores the same post in its own content store. The state sync mechanism also helps late-joining peers catch up by exchanging full post, comment, and vote snapshots.

### Q3. Why do user accounts not automatically work across replicas?

Because authentication state is local to each node. Every node persists its own `users.json` containing user-password mappings and token mappings. There is no distributed user directory, no shared auth service, and no replicated session store. The UI is built around that constraint and stores sessions per API endpoint.

### Q4. What exactly does the DHT do here if requests are not routed by it?

The DHT provides a model of which nodes are responsible for which communities. It supports consistent hashing, replication-factor lookup, and explicit assignment tracking. In the current runtime it primarily acts as a placement and metadata structure rather than a full application router. Some tests exercise it more directly than the live app does.

### Q5. How does the system stop two nodes from both becoming independent leaders for the same community?

The main prevention mechanism is the `remoteCommunities` signal populated by gossip community announcements. If a node knows another node already hosts the community, it joins as a follower instead of bootstrapping a new Raft cluster. The original leader can then add the follower as a voter. This is a practical anti-split-brain measure for the demo, though a production design would usually need stronger coordination and recovery logic.

### Q6. Are deleted posts actually removed from disk?

No. They remain in the content store and on disk. Deletion is represented by a moderation action in the Raft log. The read APIs hide deleted content by scanning that log and filtering posts or comments at response time. This is a soft-delete model.

### Q7. What happens if two users vote at the same time on different replicas?

Each replica applies the local vote to its vote CRDT state and then broadcasts that state. Peers merge the states using CRDT merge rules. Since the score is a PN counter with per-node components and each user’s latest vote is tracked with an LWW register, the replicas are expected to converge on the same final score after gossip exchange.

### Q8. Why is moderation per community instead of global?

Because the Raft cluster is created per community, and moderation actions are stored in a community-specific log. That matches the idea that a moderator policy for one community should not automatically affect another. The code and tests both enforce this isolation. A user banned in `golang` can still interact in `distributed` unless separately moderated there.

### Q9. What would have to change to make user accounts replicated too?

You would need a distributed identity mechanism. Options include:

- gossip-based account replication with conflict resolution
- a separate global Raft cluster for account registration and session issuance
- an external auth provider

The simplest architecture-consistent extension would be to add a replicated identity subsystem, but then you must solve username uniqueness, password security, conflict resolution, and token invalidation semantics across replicas.

### Q10. Why are passwords hashed with SHA-256, and why is that not enough for production?

SHA-256 is easy to implement and deterministic, which is fine for a classroom or demo setting. It is not sufficient for production password storage because it is too fast. Real password storage should use a memory-hard or work-factor-based algorithm like Argon2, scrypt, or bcrypt so offline brute force is significantly more expensive.

### Q11. How does restart recovery actually work?

There are three main recovery paths:

- the content store reloads posts, comments, and vote states from JSON files
- the node scans for `raft_<community>` directories and rejoins those communities
- Raft reloads its log and snapshots from BoltDB and snapshot storage

This is why the same node, started with the same data directory, can recover content and moderation state after a restart.

### Q12. Why does the UI sometimes show no communities on a replica even though content exists somewhere else in the cluster?

Because the UI’s community list is driven by `GET /api/communities`, and that endpoint returns the communities locally joined on the active node. It is not a cluster-wide discovery endpoint. A replica may know about content through gossip or have content in storage, but if it has not joined that community locally, the current UI will not present it as part of that replica’s joined-community list.

### Q13. What is the best way to explain this project in one sentence?

It is a distributed Reddit-style demo where discussion content spreads through gossip and CRDTs, while moderation is enforced by per-community Raft consensus.

## 25. Final Summary

This project is best understood as a teaching-oriented distributed application that intentionally combines multiple distributed systems ideas in one working stack:

- content-addressed objects
- eventually consistent replicated content
- CRDT-based vote convergence
- Raft-backed administrative consistency
- local persistence and restart recovery
- multi-replica visualization through a browser UI

It already demonstrates several nontrivial system behaviors well:

- distributed post and comment replication
- convergent vote state
- per-community moderation consensus
- replica switching and failover demos
- persistent state across restart

Its biggest current limitations are identity replication, global community discovery, and production-grade security and authorization. Those are the natural places to extend next if the goal is to move from a strong demo or class project toward a more fully featured distributed social platform.