# DReddit Demo Runbook

This guide walks through a complete DReddit demo using the replica-aware UI and the 5-node local demo cluster.

It is designed for showing:

- multi-process replicas
- multi-community behavior
- gossip replication for posts, comments, and votes
- Raft-backed moderation per community
- failover when a replica goes down
- restart persistence and session persistence

## What You Will Run

The demo cluster starts 5 separate OS processes:

- node1 on `http://localhost:8091`
- node2 on `http://localhost:8092`
- node3 on `http://localhost:8093`
- node4 on `http://localhost:8094`
- node5 on `http://localhost:8095`

Each node has:

- its own HTTP port
- its own gossip port
- its own on-disk data directory under `/tmp/dreddit_demo_cluster`
- its own local auth/session state

## Prerequisites

- Go installed and working
- No other services already bound to ports `8091-8095` or `11101-11105`
- A modern browser

## 1. Start The Demo Cluster

From the repo root:

```bash
rm -rf /tmp/dreddit_demo_cluster
cd /home/shan/ws/repos/DReddit
bash scripts/run_demo_cluster.sh
```

Expected result:

- 5 DReddit processes start
- logs are written to `/tmp/dreddit_demo_cluster/node*.log`
- each UI is reachable on `http://localhost:8091` through `http://localhost:8095`

To stop the cluster later:

```bash
pkill -f "dreddit -id demo-node"
```

## 2. Open The UI

Open:

```text
http://localhost:8091
```

In the UI:

1. Click `Use 5-Node Demo`
2. Click `Probe Cluster`
3. Verify all 5 replicas show `UP`
4. Verify the active replica shows member count `5`

Expected result:

- the right-hand control panel shows 5 replica cards
- each card lists communities once they are joined
- you can switch replicas from a single page

## 3. Important Demo Behavior

Authentication is per replica.

That means:

- logging in on node1 does not automatically log you in on node2
- the UI stores a separate session per replica
- this is useful for demoing restart persistence and replica-local auth

## 4. Suggested Demo Story

Use this order during the demo.

### Phase A: Bring Up Communities

On replica `8091`:

1. Sign up as `admin`
2. Join communities:
   - `golang`
   - `distributed`
   - `webdev`

Expected result:

- the active replica shows all 3 communities
- after probing, other replicas should eventually list those communities too

### Phase B: Show Replication Across Replicas

On replica `8091`:

1. Sign up or log in as `alice`
2. Select `d/golang`
3. Create a post

On replica `8092`:

1. Switch to node2 in the UI
2. Sign up or log in as `bob`
3. Join `d/golang`
4. Create another post

On replica `8093`:

1. Switch to node3
2. Sign up or log in as `carol`
3. Join `d/golang`
4. Open one of the posts and add a comment

Then switch to replicas `8094` and `8095`.

Expected result:

- the same posts appear on multiple replicas
- the comment appears on other replicas after refresh/polling
- all of this happens with different OS processes

### Phase C: Show Vote Convergence

Vote on the same post from multiple replicas:

- upvote from node1
- upvote from node2
- upvote from node3
- downvote from node4
- upvote from node5

Expected result:

- after switching between replicas, the displayed score converges to the same value everywhere
- this demonstrates CRDT-based vote merging

### Phase D: Show Community Isolation

Create one post in each community:

- `golang`
- `distributed`
- `webdev`

Expected result:

- `d/golang` only shows `golang` posts
- `d/distributed` only shows `distributed` posts
- `d/webdev` only shows `webdev` posts

This is important because moderation is scoped per community and each community has its own Raft group.

## 5. Moderation Demo

Log in as `admin` on the active replica.

The UI has an `Admin Console` and helper buttons on posts/comments.

### Delete Post

1. Pick a visible post
2. Click `Delete` on that post

Expected result:

- the post disappears locally
- after switching replicas, the post is gone there too

### Restore Post

1. Click `Copy Hash` on the deleted post before or during the demo, or use a known target hash
2. In `Admin Console`, choose `RESTORE_POST`
3. Paste the post hash into `Target`
4. Execute the action

Expected result:

- the post reappears across replicas

### Delete Comment

1. Expand comments on a post
2. Use `Prepare DELETE_COMMENT` on a comment
3. Execute the moderation action

Expected result:

- the comment disappears from the comment list across replicas

### Ban User

1. Use `Prepare Ban` on a non-admin author in `d/golang`
2. Execute `BAN_USER`

Expected result:

- the banned user cannot post in `golang`
- the banned user cannot rejoin `golang` as a fresh join

### Unban User

1. Use `Prepare Unban`
2. Execute `UNBAN_USER`

Expected result:

- the user can post again in `golang`

### Show Per-Community Moderation Isolation

After banning a user in `golang`:

1. Switch to `d/distributed`
2. Try posting there as the same user

Expected result:

- posting in `distributed` still works

This demonstrates that moderation is not global; it is scoped to the community’s Raft log.

## 6. Replica Failure Demo

This is the simplest live failover demo.

### Kill The Active Replica

If you are currently viewing node4:

```bash
pkill -f "dreddit -id demo-node4"
```

Expected result in the UI:

- the active replica card becomes `DOWN`
- the banner warns that the active replica is unreachable
- other replicas remain `UP`

Now click another `UP` replica card, for example node2.

Expected result:

- feed data is still available
- posts/comments/votes are still readable from the surviving replica
- the demo continues without restarting the whole system

### Restart The Failed Replica

Restart node4 with the same data directory:

```bash
cd /home/shan/ws/repos/DReddit
./dreddit -id demo-node4 -addr :8094 -gossip-port 11104 \
  -peers 127.0.0.1:11101,127.0.0.1:11102,127.0.0.1:11103 \
  -data-dir /tmp/dreddit_demo_cluster/node4 \
  > /tmp/dreddit_demo_cluster/node4.log 2>&1 &
```

Then in the UI:

1. Click `Probe Cluster`
2. Switch back to node4 once it shows `UP`

Expected result:

- node4 becomes healthy again
- it rejoins the 5-member cluster
- it shows the replicated state again

## 7. Persistence Demo

Use this to show that data survives restart.

### Restart A Replica Without Losing Data

For node3:

```bash
pkill -f "dreddit -id demo-node3"
```

Restart it:

```bash
cd /home/shan/ws/repos/DReddit
./dreddit -id demo-node3 -addr :8093 -gossip-port 11103 \
  -peers 127.0.0.1:11101,127.0.0.1:11102,127.0.0.1:11104 \
  -data-dir /tmp/dreddit_demo_cluster/node3 \
  > /tmp/dreddit_demo_cluster/node3.log 2>&1 &
```

Expected result:

- node3 comes back `UP`
- old posts and comments are still present
- a session created earlier on node3 still works after restart

## 8. Minimal Demo Script For Presentation

If you want the shortest presentable path, do this:

1. Start the 5-node cluster
2. Open node1 UI and probe cluster
3. Create 3 communities
4. Create a post on node1 and another on node2
5. Add a comment on node3
6. Vote from nodes 1 through 5
7. Show the same score on all replicas
8. Ban a user in `golang`
9. Show that the same user can still post in `distributed`
10. Kill node4
11. Show node4 goes `DOWN` while the app still works from node2
12. Restart node4
13. Show node4 comes back with persisted data

## 9. Troubleshooting

### A Replica Shows DOWN Immediately

Check the log file:

```bash
tail -100 /tmp/dreddit_demo_cluster/node4.log
```

Common causes:

- port already in use
- stale local processes still running

To clean up all demo nodes:

```bash
pkill -f "dreddit -id demo-node"
```

Then start again:

```bash
rm -rf /tmp/dreddit_demo_cluster
bash scripts/run_demo_cluster.sh
```

### The UI Still Points To The Wrong Replica List

Use the `Use 5-Node Demo` button again, then click `Probe Cluster`.

### Login Works On One Replica But Not Another

That is expected. Sessions are per replica. Log in separately on the replica where you want to write.

## 10. Files Involved In The Demo

- [ui/index.html](/home/shan/ws/repos/DReddit/ui/index.html)
- [scripts/run_demo_cluster.sh](/home/shan/ws/repos/DReddit/scripts/run_demo_cluster.sh)
- [scripts/test_cluster_full.sh](/home/shan/ws/repos/DReddit/scripts/test_cluster_full.sh)
- [internal/api/api.go](/home/shan/ws/repos/DReddit/internal/api/api.go)

## 11. Optional Verification Before A Live Demo

Run the automated full-cluster test first:

```bash
cd /home/shan/ws/repos/DReddit
go build -o dreddit ./cmd/dreddit/
bash scripts/test_cluster_full.sh
```

Expected result:

```text
101 passed, 0 failed
```