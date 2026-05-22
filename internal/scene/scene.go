package scene

import (
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/aoi"
)

type Player struct {
	ID   int64
	X, Y float32
}

// Notifier 是场景对外发通知的出口。场景本身不碰网络/协议,
// 只通过这个接口告诉上层"该把什么发给谁",由网关去编码、走 socket。
// 好处:场景能脱离网络单独做单元测试(传一个假 Notifier 记录调用即可)。
type Notifier interface {
	// BroadcastMove 把 moverID 的新位置 (x,y) 发给 viewerIDs 这些观察者。
	BroadcastMove(viewerIDs []int64, moverID int64, x, y float32)
}
type inputKind int

const (
	inputJoin  inputKind = iota // 0:玩家进入场景
	inputLeave                  // 1:玩家离开
	inputMove                   // 2:玩家移动
)

type input struct {
	kind     inputKind
	playerID int64
	x, y     float32
}
type Scene struct {
	aoiMgr   *aoi.Manager
	players  map[int64]*Player
	inCh     chan input
	tickRate time.Duration
	notifier Notifier
}

func NewScene(aoiMgr *aoi.Manager, notifier Notifier, tickHz int) *Scene {
	return &Scene{
		aoiMgr:   aoiMgr,
		players:  make(map[int64]*Player),
		inCh:     make(chan input, 1024),
		tickRate: time.Second / time.Duration(tickHz),
		notifier: notifier,
	}
}
func (s *Scene) Join(playerID int64, x, y float32) {
	s.inCh <- input{kind: inputJoin, playerID: playerID, x: x, y: y}
}
func (s *Scene) Leave(playerID int64) {
	s.inCh <- input{kind: inputLeave, playerID: playerID}
}
func (s *Scene) Move(playerID int64, x, y float32) {
	s.inCh <- input{kind: inputMove, playerID: playerID, x: x, y: y}
}

// Run 启动 tick 主循环(会阻塞,通常放在自己的 goroutine 里跑)
func (s *Scene) Run() {
	ticker := time.NewTicker(s.tickRate)
	defer ticker.Stop()
	for range ticker.C { // 每到一个 tick 时刻,ticker.C 这个通道就来一个信号
		s.tick()
	}
}
func (s *Scene) tick() {
	for {
		select {
		case in := <-s.inCh:
			s.apply(in)
		default:
			return // 通道里暂时没有更多输入了,这一帧结束
		}
	}
}
func (s *Scene) apply(in input) {
	switch in.kind {
	case inputJoin:
		s.handleJoin(in)
	case inputLeave:
		s.handleLeave(in)
	case inputMove:
		s.handleMove(in)
	}
}
func (s *Scene) handleJoin(in input) {
	p := &Player{ID: in.playerID, X: in.x, Y: in.y}
	s.players[p.ID] = p
	s.aoiMgr.Enter(p.ID, p.X, p.Y)
	// (进入视野的通知留到下一步,需要新增 proto 消息)
}
func (s *Scene) handleLeave(in input) {
	p := s.players[in.playerID]
	if p == nil {
		return
	}
	s.aoiMgr.Leave(p.ID, p.X, p.Y)
	delete(s.players, p.ID)
	// (离开视野的通知留到下一步)
}

func (s *Scene) handleMove(in input) {
	p := s.players[in.playerID]
	if p == nil {
		return // 玩家不存在(可能已离场),忽略
	}
	oldX, oldY := p.X, p.Y
	p.X, p.Y = in.x, in.y
	s.aoiMgr.Move(p.ID, oldX, oldY, p.X, p.Y) // 进/出视野的处理下一步补上

	// 关键:移动只广播给"当前视野内"的其他玩家,而不是全场 —— 这就是 AOI 的收益。
	viewers := s.aoiMgr.ViewPlayers(p.X, p.Y)
	others := make([]int64, 0, len(viewers))
	for _, id := range viewers {
		if id != p.ID { // 排除自己
			others = append(others, id)
		}
	}
	s.notifier.BroadcastMove(others, p.ID, p.X, p.Y)
}
