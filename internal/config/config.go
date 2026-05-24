// Package config holds the demo's tunable parameters in one place, so the
// world size, tick/sync rates, spawn point, and default ports can be changed
// without hunting through the code.
package config

// 模拟 / 世界参数(帧率是默认值 = "default" 预设;启动可用 -rates 选预设,运行时可在管理台改)
const (
	TickHz = 30 // 逻辑帧率(每秒 tick 数)
	AOIHz  = 10 // 给玩家的 AOI 状态同步频率(每秒,看清附近)
	AllHz  = 3  // 全场快照频率(每秒):给玩家画小地图、给观战者看全局

	MapMinX  = 0   // 地图左边界
	MapMinY  = 0   // 地图下边界
	MapMaxX  = 512 // 地图右边界
	MapMaxY  = 512 // 地图上边界
	CellSize = 32  // AOI 格子边长(约等于视野半径)
)

// RatePreset 是一组帧率(tick / AOI 同步 / 全场同步,单位 Hz)。
type RatePreset struct{ TickHz, AOIHz, AllHz int }

// RatePresets 是可选的帧率预设:用 cmd/server 的 -rates 选,或运行时在管理台改。
var RatePresets = map[string]RatePreset{
	"default": {TickHz, AOIHz, AllHz}, // 30/10/5,与 README 性能数据一致
	"high":    {64, 64, 16},           // 高帧率:AOI 每 tick,更顺但更费带宽
}

// 新玩家出生点
const (
	SpawnX float32 = 0
	SpawnY float32 = 0
)

// 默认监听地址(各 cmd 的 flag 默认值)
const (
	GameAddr  = ":9000" // 玩家 TCP 入口
	HTTPAddr  = ":8080" // HTTP 控制 API
	SceneAddr = ":9100" // 场景 gRPC
)

// BroadcastTarget 是 SceneEvent.TargetPlayerId 的哨兵值:等于它表示"广播给所有连接"。
// 网关只编码一次、复用同一个包扇出给全部连接(全局快照对所有人字节一致)。
// 合法玩家 ID 从 1 起(连接 ID 自增,0 永不分配),故 0 可安全作哨兵。
const BroadcastTarget int64 = 0
