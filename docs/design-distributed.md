# Design: Distributed Split (Gateway + Scene over gRPC)

This document covers phase 5a: splitting the single-process server into a
**gateway** process and a **scene** process that talk over gRPC. The monolith
(`cmd/server`) still exists for the simple all-in-one path; the split is an
alternative deployment (`cmd/gateway` + `cmd/scene`).

Source: [`internal/sceneserver`](../internal/sceneserver/server.go) (scene gRPC
server), [`internal/gwserver`](../internal/gwserver/server.go) (gateway gRPC
client), proto: [`proto/scene.proto`](../proto/scene.proto).

## Why split (and why this split)

Different concerns have different resource profiles and scaling needs:

- **Gateway** — IO-bound: holds many TCP connections, codecs, routing. Scales by
  adding gateway instances to spread connections.
- **Scene** — CPU/memory-bound: the tick loop, AOI, authoritative game state.
  Scales by sharding maps (one scene process per region).

Splitting also gives fault isolation (a scene crash doesn't drop connections)
and independent deploys. See [why distributed](#) discussion in the project notes;
the short version: do it when one process can no longer scale or when the parts
need to scale/fail independently — not prematurely.

The key enabler is that the scene was already **network-free** and emitted output
through a `Notifier` interface. Splitting only changes *where input comes from*
and *where output goes* — the AOI / tick / state-sync logic is unchanged.

## The wire contract

A single gRPC **bidirectional stream** carries both directions:

```proto
service SceneService {
  rpc Session(stream GatewayEvent) returns (stream SceneEvent);
}
```

- **Upstream** (`GatewayEvent`): `PlayerJoin` / `PlayerMoveReq` / `PlayerLeaveReq`.
- **Downstream** (`SceneEvent`): `{ target_player_id, msg_type, payload }` — the
  scene says "send this already-encoded message to this player"; the gateway wraps
  it in a protocol frame and writes it to that player's connection.

Why one long-lived bidi stream instead of unary RPCs: movement is high-frequency,
and the scene pushes events continuously. A stream avoids per-call overhead and
naturally models the continuous two-way flow.

## Data flow

```
client ──TCP MoveReq──▶ gateway ──GatewayEvent(stream)──▶ scene.Move
                                                              │ tick + AOI
                                                     SceneEvent(stream)
                                                              │ target=playerID
client ◀──TCP StateSync── gateway ◀───────────────────────────┘
                          (route by target_player_id, re-frame, Send)
```

## Concurrency notes (interview-relevant)

- **A gRPC stream is not safe for concurrent `Send`.** The gateway funnels all
  upstream events through a single `upstream` channel drained by one goroutine —
  the same "serialize writes through one goroutine" pattern used for the TCP
  connection's write loop.
- On the scene side, the `Notifier` runs in the tick goroutine and pushes
  `SceneEvent`s into a per-session channel; a separate goroutine drains it to
  `stream.Send`. The session never closes that channel (it nils the reference and
  signals via a `done` channel) to avoid a send-on-closed-channel panic.
- The gateway reuses the existing `gateway.Conn` machinery; it just has no
  in-process scene.

## Scope and what's deferred

- **5a (done)**: single gateway ↔ single scene, players move and see each other
  across the process boundary.
- **Deferred**: multiple gateways (route downstream by which gateway owns the
  player), map sharding across scenes, the spectator/HTTP API in distributed mode,
  the data layer (Redis/MySQL), and a battle service. These are phase 5b/5c.

## Run it

```bash
go run ./cmd/scene                 # scene gRPC server on :9100
go run ./cmd/gateway               # gateway on :9000, connects to scene
go run ./cmd/client -name alice    # players connect to the gateway as usual
```

Start the scene before the gateway.
