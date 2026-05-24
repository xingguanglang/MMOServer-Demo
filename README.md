# MMOServer-Demo

[![CI](https://github.com/xingguanglang/MMOServer-Demo/actions/workflows/ci.yml/badge.svg)](https://github.com/xingguanglang/MMOServer-Demo/actions/workflows/ci.yml)

**English** | [简体中文](README_zh.md)

A distributed MMO game-server framework written in Go, built to demonstrate the
core systems behind a real-time multiplayer world: a custom binary protocol over
TCP, a fixed-rate tick loop, and **nine-grid Area-of-Interest (AOI)** so each
player only syncs with others nearby instead of the whole map.

> Status: core feature-complete — protocol, gateway, nine-grid AOI, tick loop,
> 10 Hz state sync, ebiten client + spectator, load testing with measured data,
> a gRPC gateway/scene split, a web admin console, CI, and Docker.

## Demo

A client's view — your avatar is green; nearby players (white) update fast via
AOI, while distant ones (gray) come from the low-rate global feed:

![client view](docs/clientView.gif)

200 load-test bots — each client syncs at high rate only with the handful nearby
(white) yet still sees everyone on the map (gray) at a low rate:

![load test, 200 bots](docs/loadTest200.gif)

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
- **Distance-based update rate (LOD)**: a client sees *everyone* on the map, but
  nearby players update fast via AOI (10 Hz, bright) while distant players come
  from a low-rate (5 Hz, dim) full-scene snapshot — full awareness, relevance-
  scaled freshness.
- **Web admin console + HTTP API**: a browser dashboard (served by the Go server)
  to spawn/move players and run load tests, with live players/connections/bandwidth/
  AOI-mode metrics — drive and observe the world without the binary protocol.
- **Tested**: unit tests for the codec and AOI, plus end-to-end gateway, client,
  and HTTP-API integration tests over real connections.

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
internal/api/       HTTP/JSON control API (spawn, query) (+ tests)
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

## Distributed mode (gateway + scene)

Besides the all-in-one `cmd/server`, the server can run split into a **gateway**
process and a **scene** process talking over a gRPC bidirectional stream — the
gateway owns TCP connections, the scene owns the tick loop + AOI:

```bash
go run ./cmd/scene                 # scene gRPC server on :9100 (start first)
go run ./cmd/gateway               # gateway on :9000, connects to the scene
go run ./cmd/client -name alice    # players connect to the gateway as usual
```

See [docs/design-distributed.md](docs/design-distributed.md) for the wire
contract and rationale.

## Admin console & HTTP API

The server hosts a small **web admin console** at `http://localhost:8080/` plus a
JSON API (default `:8080`), so you can drive and observe the world from a browser
or external tools without speaking the binary protocol. The console has tabs to
spawn / move players and start a load test, and live cards for players,
connections, downstream bandwidth, and AOI mode.

| Method & path        | Body                                | Description                                          |
| -------------------- | ----------------------------------- | ---------------------------------------------------- |
| `GET /api/metrics`   | —                                   | `{players, connections, sentBytes, aoiEnabled, tickHz, aoiHz, allHz}` |
| `GET /api/players`   | —                                   | All players' positions as JSON                       |
| `POST /api/spawn`    | `{"count":50,"x":128,"y":128}`      | Spawn `count` players at (x, y); returns their `ids` |
| `POST /api/move`     | `{"id":3,"x":50,"y":60}`            | Push an API-spawned player to exact (x, y)           |
| `POST /api/loadtest` | `{"count":200}`                     | Inject `count` random-walking bots (load)            |
| `POST /api/rates`    | `{"tickHz":30,"aoiHz":10,"allHz":5}`| Change tick / AOI-sync / full-sync rates live        |
| `POST /api/aoi`      | `{"enabled":false}`                 | Toggle AOI (off = broadcast to all → bandwidth jumps)|
| `POST /api/clear`    | —                                   | Disconnect all players (spectators stay)             |
| `POST /api/launch`   | `{"spectate":true}`                 | Open a client/spectator window on the host (local)   |

Spawned players are real TCP clients, so they also appear on every client's map.

### Walkthrough

Easiest: open the console at `http://localhost:8080/` and click around. Or drive
it over HTTP:

```bash
# 1) spawn a player at an exact coordinate; the response gives its id
curl -s -X POST localhost:8080/api/spawn -d '{"count":1,"x":128,"y":128}'
#    -> {"spawned":1,"ids":[3]}

# 2) push a new coordinate to that player
curl -s -X POST localhost:8080/api/move -d '{"id":3,"x":50,"y":60}'

# 3) read the world / live metrics
curl -s localhost:8080/api/players
curl -s localhost:8080/api/metrics

# 4) generate load, then watch metrics
curl -s -X POST localhost:8080/api/loadtest -d '{"count":200}'

# 5) flip AOI off (broadcast-to-all) and watch bandwidth jump; change rates live
curl -s -X POST localhost:8080/api/aoi   -d '{"enabled":false}'
curl -s -X POST localhost:8080/api/rates -d '{"tickHz":64,"aoiHz":64,"allHz":16}'

# 6) clear the world
curl -s -X POST localhost:8080/api/clear
```

> On **Windows PowerShell**, `curl` is an alias for `Invoke-WebRequest` and won't
> accept these flags. Use `curl.exe`, or `Invoke-RestMethod`:
> ```powershell
> Invoke-RestMethod -Uri http://localhost:8080/api/spawn -Method Post `
>   -ContentType application/json -Body '{"count":1,"x":128,"y":128}'
> ```

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
- [Distributed split](docs/design-distributed.md) — gateway/scene processes over a
  gRPC bidirectional stream, the wire contract, and concurrency notes.

## Roadmap

- [x] **Phase 1** — project skeleton, binary protocol, gateway send/recv, minimal login
- [x] **Phase 2** — scene tick loop, nine-grid AOI, enter/leave view events, end-to-end sync
- [x] **Phase 3** — 10 Hz state-sync broadcast + ebiten visualization client
- [x] **Phase 4** — load-testing bots (1–2k virtual players) + performance data + AOI comparison
- [~] **Phase 5** — distributed split: gateway + scene over gRPC ✓ (5a); Redis/MySQL + battle service pending
- [x] **Phase 6** — Docker Compose, GitHub Actions CI, bilingual README + design docs
      (done ahead of phase 5; Redis/MySQL persistence lands with the distributed split)
