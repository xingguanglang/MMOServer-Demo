# Design: State Synchronization

This document explains how player state reaches clients: why state sync (not
frame sync), the 10 Hz / 30 Hz split, client-side interpolation, and how AOI
keeps the bandwidth bounded.

Source: [`internal/scene/scene.go`](../internal/scene/scene.go) (server snapshot),
[`internal/client/client.go`](../internal/client/client.go) (client world model),
[`cmd/client/main.go`](../cmd/client/main.go) (interpolation + rendering).

## State sync vs. frame sync

There are two dominant ways to keep many clients consistent:

| | **Frame sync (lockstep)** | **State sync** (this project) |
| --- | --- | --- |
| Server broadcasts | player **inputs** | player **state** (positions) |
| Who simulates | every client, identically & deterministically | the server (authoritative) |
| Bandwidth | low (just inputs) | higher (state) |
| Determinism | required across all clients | not required |
| Anti-cheat | hard (clients hold full logic) | easy (server is authority) |
| Reconnect | hard (must catch up frames) | easy (send a fresh snapshot) |
| Typical use | RTS, fighting games | MMOs, FPS |

This project uses **state sync**: the server is authoritative, simulates the
world, and broadcasts positions. For an MMO — many players, constant churn,
cheating to defend against — server authority plus state broadcast is the only
practical choice. Frame sync's requirement that every client reproduce an
identical deterministic simulation does not hold up in a large, dynamic world.

## The 10 Hz / 30 Hz split

The logic tick runs at **30 Hz** (movement is applied every tick, so simulation
stays accurate), but state is broadcast at only **10 Hz** (every 3rd tick):

```
tick:      1   2   3   4   5   6   7   8   9   ...   (30 Hz)
broadcast:         ▲           ▲           ▲         (10 Hz, every 3rd tick)
```

Why decouple them:

- **Bandwidth.** Sending positions 10×/second is plenty; 30 or 60×/second would
  multiply traffic for no visible gain.
- **Smoothness is the client's job.** The client renders at 60 fps and
  interpolates between snapshots (below), so 10 Hz on the wire still looks fluid.
- **Loss tolerance.** State sync tolerates a dropped snapshot — the next one
  (100 ms later) carries the latest absolute positions. This is why the
  connection's send queue can drop under back-pressure without corrupting state.

The two rates are independent parameters of the scene (`tickHz`, `syncHz`).

## Client-side interpolation

The server sends discrete positions 10×/second; rendering naively at those
positions would look choppy. Instead the client keeps, per remote player, a
*rendered* position that eases toward the latest *target* position from the
network, once per 60 fps frame:

```
rendered += (target - rendered) * alpha     // alpha = 0.2
```

This exponential smoothing turns 10 sparse samples per second into continuous
motion. (A more rigorous approach buffers the last two snapshots and interpolates
by timestamp; smoothing-toward-target is simpler and good enough for the demo.)

The local player is **not** interpolated — it moves directly from keyboard input
and reports its position to the server, which keeps input feeling responsive.

## How AOI bounds bandwidth

State sync's cost is bandwidth, controlled in three layers:

1. **AOI** — each player's snapshot contains only the players in its nine-grid
   view, not the whole map. This is the largest saving.
2. **Rate** — broadcast at 10 Hz, not every tick.
3. **(Future)** — only send players whose position changed (dirty flag), send
   deltas instead of full state, and quantize coordinates.

Layers 1–2 are implemented; layer 3 is a follow-up if load testing shows
bandwidth as the bottleneck.

## Message flow

```
client ──MoveReq──▶ gateway ──▶ scene.Move (apply on next 30 Hz tick)
                                      │
                          every 3rd tick (10 Hz):
                          scene.broadcastState
                                      │  per player: positions of others in AOI
                                      ▼
client ◀──StateSync── gateway.SyncState ◀── Notifier.SyncState
```

Entity lifecycle (spawn/despawn) is handled separately by `PlayerEnter` /
`PlayerLeave` events; `StateSync` only carries positions of players already
known to be in view.

## Pitfalls / notes

- **Don't conflate logic rate with network rate.** Tying broadcasts to the 30 Hz
  tick wastes bandwidth; tying the logic to 10 Hz makes simulation coarse. Keep
  them separate.
- **Snapshot excludes self.** A player's own position is authoritative locally;
  the snapshot carries only the others in view.
- **`MoveBroadcast` is now superseded.** The earlier per-move event broadcast was
  replaced by periodic `StateSync`; the message/id is kept for reference but
  unused.
