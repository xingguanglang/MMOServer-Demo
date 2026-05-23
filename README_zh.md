# MMOServer-Demo

[![CI](https://github.com/xingguanglang/MMOServer-Demo/actions/workflows/ci.yml/badge.svg)](https://github.com/xingguanglang/MMOServer-Demo/actions/workflows/ci.yml)

[English](README.md) | **简体中文**

一个用 Go 编写的分布式 MMO 游戏服务器框架,用于演示实时多人世界背后的核心系统:
基于 TCP 的自定义二进制协议、固定频率的 tick 主循环,以及**九宫格 AOI(兴趣区域)**——
让每个玩家只与附近的玩家同步,而不是整张地图的所有人。

> 状态:阶段 1–3 已完成(协议、网关、AOI、tick 主循环、端到端进/出视野、
> 10Hz 状态同步,以及 ebiten 可视化客户端)。压力测试和分布式拆分见下方路线图。

## 亮点

- **自定义二进制协议**,显式分帧(`[长度][类型][消息体]`),正确处理 TCP 粘包/拆包,
  并设有最大包长保护。
- **一连接两 goroutine 的 I/O**(一个读循环 + 一个写循环),所有发送经由 channel 串行化——
  无需对 socket 加锁即可安全并发写。
- **单 goroutine 的游戏逻辑**:30 Hz 的 tick 主循环独占玩家表和 AOI 网格,
  游戏状态只被一个 goroutine 修改,因此无需加锁。
- **九宫格 AOI**:O(附近人数) 的兴趣管理 + 相互的进/出视野事件,
  取代 O(n²) 的"广播给所有人"。
- **10Hz 状态同步 + 客户端插值**:服务器按 10Hz 广播经 AOI 过滤的位置快照,
  与 30Hz 逻辑 tick 解耦;ebiten 客户端把快照插值成 60fps 的平滑移动。
- **按距离分级更新(LOD)**:客户端能在主地图上看到**全场所有人**,但近处玩家走 AOI 高频更新(10Hz,亮色),远处玩家来自低频(5Hz,暗色)的全场快照——全局可见、新鲜度按相关性分级。
- **网页管理台 + HTTP API**:Go 服务器内置一个浏览器仪表盘,可生成/移动玩家、跑压测,并实时显示玩家数/连接数/带宽/AOI 模式——不用走二进制协议就能驱动和观察世界。
- **有测试**:协议编解码与 AOI 的单元测试,以及基于真实连接的网关、客户端、HTTP API 端到端集成测试。

## 架构

```
                ┌──────────────────────── Server(单进程) ──────────────────────────┐
                │                                                                       │
 client ──TCP──▶│  Conn (readLoop ─┐                    ┌─ Scene(tick goroutine,30Hz) │
 client ──TCP──▶│        writeLoop◀┐│                    │   持有玩家表 + AOI 网格        │
 client ──TCP──▶│                 ││  inbound  logicLoop │   join / move / leave         │
                │   ...           │└─ channel ─▶(解码并   │   计算 AOI 进/出视野           │
                │                 │            路由消息)──▶  通过 Notifier 广播 ──────────┼─▶ 回到
                │   连接表(读写锁保护的 map)        └───────────────────────────────────┤   各连接
                └───────────────────────────────────────────────────────────────────────┘
```

- **`internal/protocol`** — 帧编解码:`[4字节长度][2字节类型][protobuf 消息体]`。
- **`internal/gateway`** — 连接管理、消息路由,以及把领域事件翻译成网络消息的
  `scene.Notifier` 实现。
- **`internal/aoi`** — 纯算法(不碰网络)的九宫格 AOI 管理器。
- **`internal/scene`** — tick 主循环、玩家表、AOI 编排;不碰网络,通过 `Notifier` 接口对外输出。
- **`pkg/pb`** — Protobuf 生成代码(源文件 `proto/game.proto`)。
- **`cmd/server`** — 服务器入口。

## 协议

线路上每条消息都是一个长度前缀帧:

```
┌──────────────┬──────────────┬─────────────────────┐
│  长度 (4B)   │  类型 (2B)   │   消息体 (N 字节)     │
│  uint32 大端 │  uint16 大端 │   Protobuf 编码       │
└──────────────┴──────────────┴─────────────────────┘
       ▲              ▲
   类型 + 消息体    MsgId(见 proto/game.proto)
```

接收方先读固定的 4 字节长度,再精确读取那么多字节——这正是分帧能对抗 TCP
粘包/拆包的关键。`MaxPacketSize`(1 MiB)上限会拒绝超大帧,避免无限制内存分配。

## 技术栈

| 关注点     | 选型                          |
| ---------- | ----------------------------- |
| 语言       | Go 1.26                       |
| 传输       | TCP 长连接 + 自定义分帧       |
| 序列化     | Protocol Buffers              |
| 并发       | goroutine + channel(CSP)     |

## 目录结构

```
cmd/server/         服务器入口
cmd/client/         ebiten 可视化客户端
internal/protocol/  帧编解码(含测试)
internal/gateway/   连接管理、路由、Notifier 实现(含集成测试)
internal/aoi/       九宫格 AOI 管理器(含测试)
internal/scene/     tick 主循环 + 场景编排(含测试)
internal/client/    可复用的客户端网络层 + 世界模型(含测试)
internal/api/       HTTP/JSON 控制 API(生成、查询)(含测试)
pkg/pb/             protobuf 生成代码
proto/              protobuf 源文件(game.proto)
```

## 快速开始

前置:Go 1.26+。

```bash
# 启动服务器(监听 :9000)
go run ./cmd/server

# 在另外的终端里运行两个可视化客户端
go run ./cmd/client -name alice
go run ./cmd/client -name bob

# 运行所有测试
go test ./...

# 静态检查
go vet ./...
```

用 WASD / 方向键移动。一个客户端走近另一个时会出现在对方窗口里,走出 AOI 格子就消失。
移动之所以平滑,是因为 10Hz 的服务器快照被插值成了 60fps。同样的行为也由
`internal/gateway/server_test.go` 和 `internal/client/client_test.go` 的集成测试无头验证。

### 重新生成 Protobuf 代码

```bash
# 一次性:安装 Go 插件(版本与运行时一致)
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11

# 从 proto/game.proto 重新生成 pkg/pb/game.pb.go
protoc --go_out=. --go_opt=module=github.com/xingguanglang/MMOServer-Demo proto/game.proto
```

## 分布式模式(网关 + 场景)

除了一体化的 `cmd/server`,服务器还能拆成**网关**进程和**场景**进程,二者用 gRPC
双向流通信——网关管 TCP 连接,场景管 tick 主循环 + AOI:

```bash
go run ./cmd/scene                 # 场景 gRPC 服务,:9100(先起)
go run ./cmd/gateway               # 网关 :9000,连接场景
go run ./cmd/client -name alice    # 玩家照常连网关
```

线路契约与设计理由见 [docs/design-distributed.md](docs/design-distributed.md)。

## 管理台 & HTTP API

服务器在 `http://localhost:8080/` 内置一个**网页管理台**,外加 JSON API(默认 `:8080`),
浏览器或外部工具不走二进制协议就能驱动和观察世界。管理台有"生成/移动玩家、开压测"的标签页,
以及玩家数、连接数、下行带宽、AOI 模式的实时卡片。

| 方法与路径           | 请求体                            | 说明                                   |
| ------------------- | --------------------------------- | -------------------------------------- |
| `GET /api/metrics`  | —                                 | `{players, connections, sentBytes, aoiEnabled}` |
| `POST /api/spawn`   | `{"count":50,"x":128,"y":128}`    | 在精确 (x, y) 生成 count 个玩家,返回其 `ids` |
| `POST /api/move`    | `{"id":3,"x":50,"y":60}`          | 把 API 生成的某玩家直接推到精确 (x, y)     |
| `POST /api/loadtest`| `{"count":200}`                   | 注入 count 个随机游走的机器人(压测)      |
| `GET /api/players`  | —                                 | 全场所有玩家位置(JSON)                 |

生成的玩家是真实 TCP 客户端,所以也会出现在每个客户端的地图上。

```bash
# 生成一个并拿到 id,再把坐标推给它
curl -s -X POST localhost:8080/api/spawn -d '{"count":1,"x":128,"y":128}'   # -> {"spawned":1,"ids":[3]}
curl -X POST localhost:8080/api/move -d '{"id":3,"x":50,"y":60}'
curl localhost:8080/api/players
```

## 性能

在本地开发机、loopback 上测得(Go 1.26),地图 256×256,8×8 AOI 网格(格子 32),
30Hz tick,10Hz 状态同步。机器人随机游走(每 100ms 移动一次);`cmd/loadtest`
汇总所有机器人的每秒收帧数和下行字节数。

**并发(AOI 开)**

| 虚拟玩家 | 失败 | 每秒收帧 | 下行带宽 |
| -------- | ---- | -------- | -------- |
| 200      | 0    | ~17k     | ~0.8 MB/s |
| 1000     | 0    | ~180k    | ~2.4 MB/s |

**AOI 开 vs 全场广播(200 玩家)**

| 模式                  | 下行带宽 |
| --------------------- | -------- |
| AOI 开                | ~0.8 MB/s |
| AOI 关(广播给所有人)  | ~5.6 MB/s |

AOI 把下行带宽削减约 7 倍,正好对应九宫格"只看 9/64 地图"的视野占比(每个玩家
只同步 64 格中自己周围的 9 格)。地图越大,单个玩家视野占全图的比例越小,AOI 优势越明显。

复现:

```bash
go run ./cmd/server                       # AOI 开(默认)
# 或:go run ./cmd/server -aoi=false      # 全场广播对照组
go run ./cmd/loadtest -n 200 -duration 15s
```

## 设计文档

- [九宫格 AOI](docs/design-aoi.md) — 为什么用网格 AOI、格子大小选型、进/出视野算法、
  替代方案与踩坑记录。
- [状态同步](docs/design-sync.md) — 状态同步 vs 帧同步、10Hz / 30Hz 拆分、
  客户端插值,以及 AOI 如何限制带宽。
- [分布式拆分](docs/design-distributed.md) — 网关/场景进程经 gRPC 双向流通信、
  线路契约与并发要点。

## 路线图

- [x] **阶段 1** — 项目骨架、二进制协议、网关收发、最小登录
- [x] **阶段 2** — 场景 tick 主循环、九宫格 AOI、进/出视野事件、端到端同步
- [x] **阶段 3** — 10 Hz 状态同步广播 + ebiten 可视化客户端
- [x] **阶段 4** — 压测机器人(1–2k 虚拟玩家)+ 性能数据 + AOI 对比
- [~] **阶段 5** — 分布式拆分:网关 + 场景经 gRPC 拆开 ✓(5a);Redis/MySQL + 战斗服待做
- [x] **阶段 6** — Docker Compose、GitHub Actions CI、中英 README + 设计文档
      (先于阶段 5 完成;Redis/MySQL 持久化随分布式拆分一起做)
