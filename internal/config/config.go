// Package config holds the demo's tunable parameters in one place, so the
// world size, tick/sync rates, spawn point, and default ports can be changed
// without hunting through the code.
package config

// 模拟 / 世界参数
const (
	TickHz = 30 // 逻辑帧率(每秒 tick 数)
	AOIHz  = 10 // 给玩家的 AOI 状态同步频率(每秒)
	AllHz  = 10 // 给观战者的全场快照频率(每秒)

	MapMinX  = 0   // 地图左边界
	MapMinY  = 0   // 地图下边界
	MapMaxX  = 256 // 地图右边界
	MapMaxY  = 256 // 地图上边界
	CellSize = 32  // AOI 格子边长(约等于视野半径)
)

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
