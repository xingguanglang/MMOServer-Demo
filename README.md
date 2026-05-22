# MMOServer-Demo

**English** | [简体中文](README_zh.md)

A distributed MMO game-server framework written in Go, built to demonstrate the
core systems behind a real-time multiplayer world: a custom binary protocol over
TCP, a fixed-rate tick loop, and **nine-grid Area-of-Interest (AOI)** so each
player only syncs with others nearby instead of the whole map.

> Status: phases 1–2 complete (protocol, gateway, AOI, tick loop, end-to-end
> enter/leave). Visualization client, load testing, and the distributed split
> are on the roadmap below.

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
- **Tested**: unit tests for the codec and AOI, plus end-to-end gateway
  integration tests over real TCP connections.

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
internal/protocol/  frame codec (+ tests)
internal/gateway/   connection mgmt, routing, Notifier impl (+ integration tests)
internal/aoi/       nine-grid AOI manager (+ tests)
internal/scene/     tick loop + scene orchestration (+ tests)
pkg/pb/             generated protobuf code
proto/              protobuf source (game.proto)
```

## Quick start

Prerequisites: Go 1.26+.

```bash
# Run the server (listens on :9000)
go run ./cmd/server

# Run all tests
go test ./...

# Static analysis
go vet ./...
```

There is no visual client yet (see the roadmap); the end-to-end behavior
"walk closer → a player appears, walk away → it disappears" is exercised by the
gateway integration tests in `internal/gateway/server_test.go`.

### Regenerating Protobuf code

```bash
# one-time: install the Go plugin (version pinned to the runtime)
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11

# regenerate pkg/pb/game.pb.go from proto/game.proto
protoc --go_out=. --go_opt=module=github.com/xingguanglang/MMOServer-Demo proto/game.proto
```

## Design docs

- [Nine-grid AOI](docs/design-aoi.md) — why grid AOI, cell-size selection,
  enter/leave algorithm, alternatives, and pitfalls.

## Roadmap

- [x] **Phase 1** — project skeleton, binary protocol, gateway send/recv, minimal login
- [x] **Phase 2** — scene tick loop, nine-grid AOI, enter/leave view events, end-to-end sync
- [ ] **Phase 3** — 10 Hz state-sync broadcast + ebiten visualization client (record AOI GIF)
- [ ] **Phase 4** — load-testing bots (1–2k virtual players) + performance data
- [ ] **Phase 5** — distributed split (gateway / scene / battle), gRPC, Redis + MySQL
- [ ] **Phase 6** — Docker Compose, GitHub Actions CI, full docs
