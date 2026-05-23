# MMOServer-Demo

**English** | [简体中文](README_zh.md)

A distributed MMO game-server framework written in Go, built to demonstrate the
core systems behind a real-time multiplayer world: a custom binary protocol over
TCP, a fixed-rate tick loop, and **nine-grid Area-of-Interest (AOI)** so each
player only syncs with others nearby instead of the whole map.

> Status: phases 1–3 complete (protocol, gateway, AOI, tick loop, end-to-end
> enter/leave, 10 Hz state sync, and an ebiten visualization client). Load
> testing and the distributed split are on the roadmap below.

## Highlights

- **Custom binary protocol** with explicit framing (`[length][type][body]`) that
  correctly handles TCP stickiness/fragmentation, with a max-packet guard.
- **Goroutine-per-connection I/O** (one read loop + one write loop each), with
  all sends serialized through a channel — safe concurrent writes without locks
  on the socket.
- **Single-goroutine game logic**: a 30 Hz tick loop owns the player table and
  the AOI grid, so game state is mutated by exactly one goroutine and needs no
  locks.
- **Nine-grid AOI**: O(nearby) interest management with mutual enter/leave view
  events, instead of O(n²) broadcast-to-everyone.
- **10 Hz state sync + client interpolation**: the server broadcasts AOI-filtered
  position snapshots at 10 Hz, decoupled from the 30 Hz logic tick; the ebiten
  client interpolates them to smooth 60 fps movement.
- **Tested**: unit tests for the codec and AOI, plus end-to-end gateway and
  client integration tests over real TCP connections.

## Architecture

```
                ┌──────────────────────── Server (single process) ────────────────────────┐
                │                                                                            │
 client ──TCP──▶│  Conn (readLoop ─┐                       ┌─ Scene (tick goroutine, 30 Hz) │
 client ──TCP──▶│        writeLoop◀┐│                       │   owns player table + AOI grid │
 client ──TCP──▶│                 ││  inbound   logicLoop   │   join / move / leave           │
                │   ...           │└─ channel ─▶ (decode &   │   computes AOI enter/leave      │
                │                 │             route msgs)──▶   broadcasts via Notifier ──────┼─▶ back to
                │   connection registry (mutex-guarded map)  └────────────────────────────────┤   each conn
                └────────────────────────────────────────────────────────────────────────────┘
```

- **`internal/protocol`** — frame codec: `[4-byte length][2-byte type][protobuf body]`.
- **`internal/gateway`** — connection management, message routing, and the
  `scene.Notifier` implementation that turns domain events into wire messages.
- **`internal/aoi`** — pure (network-free) nine-grid AOI manager.
- **`internal/scene`** — the tick loop, player table, and AOI orchestration;
  network-free, emits output through a `Notifier` interface.
- **`pkg/pb`** — generated Protobuf messages (`proto/game.proto`).
- **`cmd/server`** — the server entry point.

## Protocol

Every message on the wire is a single length-prefixed frame:

```
┌──────────────┬──────────────┬─────────────────────┐
│  length (4B) │  type (2B)   │   body (N bytes)     │
│  uint32 BE   │  uint16 BE   │   Protobuf-encoded   │
└──────────────┴──────────────┴─────────────────────┘
       ▲              ▲
   type + body    MsgId (see proto/game.proto)
```

The receiver reads the fixed 4-byte length first, then reads exactly that many
bytes — this is what makes framing robust against TCP coalescing/splitting.
A `MaxPacketSize` (1 MiB) guard rejects oversized frames to avoid unbounded
allocation.

## Tech stack

| Concern        | Choice                                  |
| -------------- | --------------------------------------- |
| Language       | Go 1.26                                 |
| Transport      | TCP long connection + custom framing    |
| Serialization  | Protocol Buffers                        |
| Concurrency    | goroutines + channels (CSP)             |

## Project layout

```
cmd/server/         server entry point
cmd/client/         ebiten visualization client
internal/protocol/  frame codec (+ tests)
internal/gateway/   connection mgmt, routing, Notifier impl (+ integration tests)
internal/aoi/       nine-grid AOI manager (+ tests)
internal/scene/     tick loop + scene orchestration (+ tests)
internal/client/    reusable network-layer client + world model (+ tests)
pkg/pb/             generated protobuf code
proto/              protobuf source (game.proto)
```

## Quick start

Prerequisites: Go 1.26+.

```bash
# Run the server (listens on :9000)
go run ./cmd/server

# In separate terminals, run two visualization clients
go run ./cmd/client -name alice
go run ./cmd/client -name bob

# Run all tests
go test ./...

# Static analysis
go vet ./...
```

Move with WASD / arrow keys. As one client moves near another it appears in the
other's window; cross out of the AOI grid cell and it disappears. Movement stays
smooth because the 10 Hz server snapshots are interpolated to 60 fps. The same
behavior is also exercised headlessly by the integration tests in
`internal/gateway/server_test.go` and `internal/client/client_test.go`.

### Regenerating Protobuf code

```bash
# one-time: install the Go plugin (version pinned to the runtime)
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11

# regenerate pkg/pb/game.pb.go from proto/game.proto
protoc --go_out=. --go_opt=module=github.com/xingguanglang/MMOServer-Demo proto/game.proto
```

## Performance

Measured on a local dev machine over loopback (Go 1.26), 256×256 map, 8×8 AOI
grid (cell 32), 30 Hz tick, 10 Hz state sync. Bots random-walk (move every
100 ms); `cmd/loadtest` reports frames/sec and downstream bytes summed across
all bots.

**Concurrency (AOI on)**

| Virtual players | Failed | Frames/s (recv) | Downstream |
| --------------- | ------ | --------------- | ---------- |
| 200             | 0      | ~17k            | ~0.8 MB/s  |
| 1000            | 0      | ~180k           | ~2.4 MB/s  |

**AOI vs. broadcast-to-all (200 players)**

| Mode                          | Downstream |
| ----------------------------- | ---------- |
| AOI on                        | ~0.8 MB/s  |
| AOI off (broadcast everyone)  | ~5.6 MB/s  |

AOI cuts downstream bandwidth ~7×, matching the nine-grid's 9/64 view-area ratio
(each player syncs only its 9 cells out of 64). The advantage grows on larger
maps, where a player's view is a smaller fraction of the world.

Reproduce:

```bash
go run ./cmd/server                       # AOI on (default)
# or: go run ./cmd/server -aoi=false      # broadcast-to-all baseline
go run ./cmd/loadtest -n 200 -duration 15s
```

## Design docs

- [Nine-grid AOI](docs/design-aoi.md) — why grid AOI, cell-size selection,
  enter/leave algorithm, alternatives, and pitfalls.
- [State sync](docs/design-sync.md) — state sync vs. frame sync, the 10 Hz / 30 Hz
  split, client-side interpolation, and how AOI bounds bandwidth.

## Roadmap

- [x] **Phase 1** — project skeleton, binary protocol, gateway send/recv, minimal login
- [x] **Phase 2** — scene tick loop, nine-grid AOI, enter/leave view events, end-to-end sync
- [x] **Phase 3** — 10 Hz state-sync broadcast + ebiten visualization client
- [x] **Phase 4** — load-testing bots (1–2k virtual players) + performance data + AOI comparison
- [ ] **Phase 5** — distributed split (gateway / scene / battle), gRPC, Redis + MySQL
- [ ] **Phase 6** — Docker Compose, GitHub Actions CI, full docs
