package scene

import (
	"sync/atomic"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/aoi"
)

type Player struct {
	ID   int64
	X, Y float32
}

type PlayerState struct {
	ID   int64
	X, Y float32
}

// Notifier 是场景对外发通知的出口。场景本身不碰网络/协议,
// 只通过这个接口告诉上层"该把什么发给谁",由网关去编码、走 socket。
// 好处:场景能脱离网络单独做单元测试(传一个假 Notifier 记录调用即可)。
type Notifier interface {
	// SyncState 把 observerID 视野内其他玩家的位置快照发给它(10Hz 状态同步)。
	SyncState(observerID int64, states []PlayerState)
	// NotifyEnter 告诉 observerID:subjectID 进入了它的视野(带坐标)。
	NotifyEnter(observerID, subjectID int64, x, y float32)
	// NotifyLeave 告诉 observerID:subjectID 离开了它的视野。
	NotifyLeave(observerID, subjectID int64)
	// SyncAll 把全场所有玩家的位置快照发出去(上帝视角观战,不过 AOI)。
	SyncAll(states []PlayerState)
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
	notifier Notifier

	tickCount int // tick 计数器(只在 tick goroutine 里用)

	// 速率用原子量存,这样可以被 HTTP 请求运行时修改、被 tick goroutine 读取,无需锁。
	// (游戏状态——玩家表/AOI——仍然只在 tick goroutine 里改,保持无锁。)
	tickHz     atomic.Int64
	aoiHz      atomic.Int64
	allHz      atomic.Int64
	aoiEnabled atomic.Bool // 运行时可切:false 时 broadcastAOI 退回全场广播
}

func NewScene(aoiMgr *aoi.Manager, notifier Notifier, tickHz int, allHz int, aoiHz int, aoiEnabled bool) *Scene {
	s := &Scene{
		aoiMgr:   aoiMgr,
		players:  make(map[int64]*Player),
		inCh:     make(chan input, 1024),
		notifier: notifier,
	}
	s.tickHz.Store(int64(tickHz))
	s.aoiHz.Store(int64(aoiHz))
	s.allHz.Store(int64(allHz))
	s.aoiEnabled.Store(aoiEnabled)
	return s
}

// SetAOIEnabled 运行时切换 AOI(false = 退回全场广播,用于对比)。
func (s *Scene) SetAOIEnabled(enabled bool) { s.aoiEnabled.Store(enabled) }

// AOIEnabled 返回当前是否开启 AOI。
func (s *Scene) AOIEnabled() bool { return s.aoiEnabled.Load() }

// SetRates 运行时修改帧率(<1 的值忽略)。
func (s *Scene) SetRates(tickHz, aoiHz, allHz int) {
	if tickHz >= 1 {
		s.tickHz.Store(int64(tickHz))
	}
	if aoiHz >= 1 {
		s.aoiHz.Store(int64(aoiHz))
	}
	if allHz >= 1 {
		s.allHz.Store(int64(allHz))
	}
}

// Rates 返回当前的三个帧率(供管理台显示)。
func (s *Scene) Rates() (tickHz, aoiHz, allHz int) {
	return int(s.tickHz.Load()), int(s.aoiHz.Load()), int(s.allHz.Load())
}

// tickInterval 由当前 tickHz 算出每帧间隔。
func (s *Scene) tickInterval() time.Duration {
	hz := s.tickHz.Load()
	if hz < 1 {
		hz = 1
	}
	return time.Second / time.Duration(hz)
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

// Run 启动 tick 主循环(会阻塞,通常放在自己的 goroutine 里跑)。
// 每帧结束后检查 tickHz 是否被改过,改了就重置定时器,实现运行时变速。
func (s *Scene) Run() {
	cur := s.tickInterval()
	ticker := time.NewTicker(cur)
	defer ticker.Stop()
	for range ticker.C {
		s.tick()
		if want := s.tickInterval(); want != cur {
			cur = want
			ticker.Reset(cur)
		}
	}
}
func (s *Scene) tick() {
	s.drainInputs()
	s.tickCount++
	tickHz := s.tickHz.Load()
	// every = tickHz/同步Hz;若同步频率 >= tickHz 则每帧都发(every<1 视为 1)。
	if aoiHz := s.aoiHz.Load(); aoiHz > 0 {
		if e := int(tickHz / aoiHz); e < 1 || s.tickCount%e == 0 {
			s.broadcastAOI()
		}
	}
	if allHz := s.allHz.Load(); allHz > 0 {
		if e := int(tickHz / allHz); e < 1 || s.tickCount%e == 0 {
			s.broadcastAll()
		}
	}
}
func (s *Scene) drainInputs() {
	for {
		select {
		case in := <-s.inCh:
			s.apply(in)
		default:
			return
		}
	}
}
func (s *Scene) broadcastAOI() {
	for id, p := range s.players {
		var states []PlayerState
		if s.aoiEnabled.Load() {
			viewers := s.aoiMgr.ViewPlayers(p.X, p.Y)
			states = make([]PlayerState, 0, len(viewers))
			for _, vid := range viewers {
				if vid == id {
					continue
				}
				if other := s.players[vid]; other != nil {
					states = append(states, PlayerState{ID: other.ID, X: other.X, Y: other.Y})
				}
			}
		} else {
			states = make([]PlayerState, 0, len(s.players))
			for oid, other := range s.players {
				if oid != id {
					states = append(states, PlayerState{ID: other.ID, X: other.X, Y: other.Y})
				}
			}
		}
		// 即使视野为空也发一条空快照:作为权威对账,让客户端把离开视野的人剔除——
		// 即便对应的 PlayerLeave 在过载丢帧时丢失,下一次同步也能自愈(消除「鬼影」)。
		s.notifier.SyncState(id, states)
	}
}

// broadcastAll 给观战者发一份全场全量快照(低频,因为它最贵)。
func (s *Scene) broadcastAll() {
	all := make([]PlayerState, 0, len(s.players))
	for _, p := range s.players {
		all = append(all, PlayerState{ID: p.ID, X: p.X, Y: p.Y})
	}
	s.notifier.SyncAll(all)
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
	// Enter 返回进场时视野内"已经在场"的其他玩家。
	inView := s.aoiMgr.Enter(p.ID, p.X, p.Y)
	s.players[p.ID] = p

	for _, otherID := range inView {
		other := s.players[otherID]
		if other == nil {
			continue
		}
		// 视野相互:新玩家看到已有玩家,已有玩家也看到新玩家。
		s.notifier.NotifyEnter(p.ID, other.ID, other.X, other.Y)
		s.notifier.NotifyEnter(other.ID, p.ID, p.X, p.Y)
	}
}
func (s *Scene) handleLeave(in input) {
	p := s.players[in.playerID]
	if p == nil {
		return
	}
	wasInView := s.aoiMgr.Leave(p.ID, p.X, p.Y)
	delete(s.players, p.ID)
	for _, otherID := range wasInView {
		s.notifier.NotifyLeave(otherID, p.ID)
	} // 告诉他们:p 消失了
	// (离开视野的通知留到下一步)
}

func (s *Scene) handleMove(in input) {
	p := s.players[in.playerID]
	if p == nil {
		return
	}
	oldX, oldY := p.X, p.Y
	p.X, p.Y = in.x, in.y

	// 跨格才有进/出视野变化;同格移动 entered/left 都为空。
	entered, left := s.aoiMgr.Move(p.ID, oldX, oldY, p.X, p.Y)
	for _, otherID := range entered {
		other := s.players[otherID]
		if other == nil {
			continue
		}
		s.notifier.NotifyEnter(p.ID, other.ID, other.X, other.Y)
		s.notifier.NotifyEnter(other.ID, p.ID, p.X, p.Y)
	}
	for _, otherID := range left {
		s.notifier.NotifyLeave(p.ID, otherID)
		s.notifier.NotifyLeave(otherID, p.ID)
	}
	// 位置变化由 10Hz 状态同步广播,这里不再逐次广播。
}
