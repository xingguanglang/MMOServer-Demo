package gateway

import (
	"log"
	"net"
	"sync"

	"github.com/xingguanglang/MMOServer-Demo/internal/aoi"
	"github.com/xingguanglang/MMOServer-Demo/internal/config"
	"github.com/xingguanglang/MMOServer-Demo/internal/protocol"
	"github.com/xingguanglang/MMOServer-Demo/internal/scene"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/protobuf/proto"
)

// 消息类型编号,与 proto 里的 MsgId 枚举保持一致。客户端和服务器共用同一套。
const (
	MsgLoginReq      uint16 = 1
	MsgLoginResp     uint16 = 2
	MsgMoveReq       uint16 = 3
	MsgMoveBroadcast uint16 = 4
	MsgPlayerEnter   uint16 = 5
	MsgPlayerLeave   uint16 = 6
	MsgStateSync     uint16 = 7
	MsgSpectate      uint16 = 8
)

// Server 是单体服务器:网关 + 登录 + 场景都在一个进程里。
// 它同时实现了 scene.Notifier,作为场景与网络之间的"翻译层"。
type Server struct {
	inbound chan Inbound   // 所有连接收到的消息都汇聚到这里
	scene   *scene.Scene   // 游戏场景(持有 tick 主循环 + AOI)

	// 连接表 + 观战者表。被 accept goroutine、看门狗 goroutine、逻辑/场景 goroutine 共享,用读写锁保护。
	mu           sync.RWMutex
	conns        map[uint64]*Conn
	spectators   map[uint64]*Conn     // 上帝视角观战的连接(收全量状态,不是玩家)
	nextID       uint64
	lastSnapshot []scene.PlayerState  // 最近一次全量状态快照,供 HTTP API 查询
}

// NewServer 创建服务器,并把场景挂上(场景以本 Server 作为 Notifier)。
// aoiEnabled=false 时场景退回"全场广播",用于 AOI 性能对比。
func NewServer(aoiEnabled bool) *Server {
	s := &Server{
		inbound:    make(chan Inbound, 1024),
		conns:      make(map[uint64]*Conn),
		spectators: make(map[uint64]*Conn),
	}
	aoiMgr := aoi.NewManager(config.MapMinX, config.MapMinY, config.MapMaxX, config.MapMaxY, config.CellSize)
	s.scene = scene.NewScene(aoiMgr, s, config.TickHz, config.AllHz, config.AOIHz, aoiEnabled)
	return s
}

// Serve 在给定的 listener 上开始服务。接收 net.Listener 而非地址字符串,便于测试。
func (s *Server) Serve(ln net.Listener) error {
	go s.scene.Run()  // 场景 tick 主循环(游戏状态只在这个 goroutine 里被改 → 无锁)
	go s.logicLoop()  // 网关逻辑 goroutine:解码消息、路由到场景

	for {
		raw, err := ln.Accept()
		if err != nil {
			return err
		}

		s.nextID++ // accept 循环单 goroutine,自增不用加锁
		c := NewConn(s.nextID, raw, s.inbound)
		s.addConn(c)
		c.Start()
		log.Printf("conn %d connected", c.ID())

		// 看门狗:连接结束后,通知场景该玩家离场,并从连接表移除。
		go func(c *Conn) {
			<-c.Done()
			s.scene.Leave(int64(c.ID()))
			s.removeConn(c.ID())
			log.Printf("conn %d disconnected", c.ID())
		}(c)
	}
}

func (s *Server) addConn(c *Conn) {
	s.mu.Lock()
	s.conns[c.ID()] = c
	s.mu.Unlock()
}

func (s *Server) removeConn(id uint64) {
	s.mu.Lock()
	delete(s.conns, id)
	delete(s.spectators, id)
	s.mu.Unlock()
}

// connByPlayer 按玩家 ID 找连接。阶段 2 里玩家 ID 就等于连接 ID。
func (s *Server) connByPlayer(playerID int64) *Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conns[uint64(playerID)]
}

// Snapshot 返回最近一次全量玩家状态快照(供 HTTP API 查询全场玩家位置)。
func (s *Server) Snapshot() []scene.PlayerState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSnapshot
}

// logicLoop 串行消费 inbound 通道,把消息解码后路由给场景。
func (s *Server) logicLoop() {
	for in := range s.inbound {
		s.handle(in)
	}
}

func (s *Server) handle(in Inbound) {
	switch in.MsgType {
	case MsgLoginReq:
		s.handleLogin(in)
	case MsgMoveReq:
		s.handleMove(in)
	case MsgSpectate:
		s.handleSpectate(in)
	default:
		log.Printf("conn %d: unknown msg type %d", in.Conn.ID(), in.MsgType)
	}
}

// handleLogin 处理登录:用连接 ID 当玩家 ID,登录必成功,然后让玩家进入场景。
func (s *Server) handleLogin(in Inbound) {
	var req pb.LoginReq
	if err := proto.Unmarshal(in.Body, &req); err != nil {
		log.Printf("conn %d: bad LoginReq: %v", in.Conn.ID(), err)
		return
	}
	playerID := int64(in.Conn.ID())
	s.sendTo(in.Conn, MsgLoginResp, &pb.LoginResp{
		PlayerId: playerID,
		Success:  true,
		Message:  "welcome " + req.GetUsername(),
	})
	// 进入场景:场景会通过 Notifier 回调,把视野内已有玩家以 PlayerEnter 推给它,反之亦然。
	s.scene.Join(playerID, config.SpawnX, config.SpawnY)
	log.Printf("conn %d logged in as %q", in.Conn.ID(), req.GetUsername())
}

// handleMove 处理移动:转交给场景,由场景按 AOI 决定广播给谁、产生进/出视野事件。
func (s *Server) handleMove(in Inbound) {
	var req pb.MoveReq
	if err := proto.Unmarshal(in.Body, &req); err != nil {
		log.Printf("conn %d: bad MoveReq: %v", in.Conn.ID(), err)
		return
	}
	s.scene.Move(int64(in.Conn.ID()), req.GetX(), req.GetY())
}

// handleSpectate 把一条连接标记为观战者:它不作为玩家进入场景,
// 而是每个广播 tick 收到全场所有玩家的全量状态(上帝视角)。
func (s *Server) handleSpectate(in Inbound) {
	s.mu.Lock()
	s.spectators[in.Conn.ID()] = in.Conn
	s.mu.Unlock()
	log.Printf("conn %d is now a spectator", in.Conn.ID())
}

// ---- 实现 scene.Notifier:把场景的领域事件翻译成协议消息,发给对应玩家 ----

// SyncState 把某玩家视野内其他玩家的位置快照,编码成 StateSync 发给它(10Hz)。
func (s *Server) SyncState(observerID int64, states []scene.PlayerState) {
	players := make([]*pb.PlayerState, 0, len(states))
	for _, st := range states {
		players = append(players, &pb.PlayerState{PlayerId: st.ID, X: st.X, Y: st.Y})
	}
	s.sendToPlayer(observerID, MsgStateSync, &pb.StateSync{Players: players})
}

// SyncAll 缓存最新全量快照(供 HTTP API 查询),并发给所有观战者(上帝视角)。
func (s *Server) SyncAll(states []scene.PlayerState) {
	s.mu.Lock()
	s.lastSnapshot = states // scene 每 tick 传入新切片、之后不再改动,存引用安全
	hasSpectators := len(s.spectators) > 0
	var targets []*Conn
	if hasSpectators {
		targets = make([]*Conn, 0, len(s.spectators))
		for _, c := range s.spectators {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()

	if !hasSpectators {
		return
	}

	players := make([]*pb.PlayerState, 0, len(states))
	for _, st := range states {
		players = append(players, &pb.PlayerState{PlayerId: st.ID, X: st.X, Y: st.Y})
	}
	packet, err := encodeMessage(MsgStateSync, &pb.StateSync{Players: players})
	if err != nil {
		log.Printf("encode error: %v", err)
		return
	}
	for _, c := range targets {
		c.Send(packet)
	}
}

// NotifyEnter 告诉 observer:subject 进入了它的视野(带 subject 的坐标)。
func (s *Server) NotifyEnter(observerID, subjectID int64, x, y float32) {
	s.sendToPlayer(observerID, MsgPlayerEnter, &pb.PlayerEnter{PlayerId: subjectID, X: x, Y: y})
}

// NotifyLeave 告诉 observer:subject 离开了它的视野。
func (s *Server) NotifyLeave(observerID, subjectID int64) {
	s.sendToPlayer(observerID, MsgPlayerLeave, &pb.PlayerLeave{PlayerId: subjectID})
}

// sendToPlayer 把消息发给指定玩家(若其连接已不在则忽略)。
func (s *Server) sendToPlayer(playerID int64, msgType uint16, msg proto.Message) {
	if c := s.connByPlayer(playerID); c != nil {
		s.sendTo(c, msgType, msg)
	}
}

// sendTo 把一个 protobuf 消息编码后发给指定连接。
func (s *Server) sendTo(c *Conn, msgType uint16, msg proto.Message) {
	packet, err := encodeMessage(msgType, msg)
	if err != nil {
		log.Printf("encode error: %v", err)
		return
	}
	c.Send(packet)
}

// encodeMessage 把 protobuf 消息序列化,再套上我们的 [长度][类型][体] 协议帧。
func encodeMessage(msgType uint16, msg proto.Message) ([]byte, error) {
	body, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return protocol.Encode(msgType, body)
}
