// Package sceneserver wraps the in-process scene.Scene behind the SceneService
// gRPC interface. A gateway opens one Session bidi stream: it sends player
// events upstream (join/move/leave) and receives the resulting messages
// downstream (state sync / enter / leave), already encoded and addressed to a
// target player id.
package sceneserver

import (
	"log"
	"sync"

	"github.com/xingguanglang/MMOServer-Demo/internal/aoi"
	"github.com/xingguanglang/MMOServer-Demo/internal/scene"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/protobuf/proto"
)

const (
	msgStateSync   = uint32(pb.MsgId_MSG_STATE_SYNC)
	msgPlayerEnter = uint32(pb.MsgId_MSG_PLAYER_ENTER)
	msgPlayerLeave = uint32(pb.MsgId_MSG_PLAYER_LEAVE)
)

// Server 实现 pb.SceneServiceServer,内部持有一个 scene.Scene,并作为它的 Notifier。
// 5a 假设单网关↔单场景:同一时刻只有一条会话,out 指向当前会话的下行通道。
type Server struct {
	pb.UnimplementedSceneServiceServer
	scene *scene.Scene

	mu  sync.Mutex
	out chan<- *pb.SceneEvent // 当前会话的下行通道;无会话时为 nil
}

// New 创建场景服务,并启动 tick 主循环。
func New() *Server {
	s := &Server{}
	aoiMgr := aoi.NewManager(0, 0, 256, 256, 32)
	s.scene = scene.NewScene(aoiMgr, s, 30, 10, 10, true) // 30Hz tick;全场/AOI 均 10Hz
	go s.scene.Run()
	return s
}

// ---- scene.Notifier:把场景的领域事件编码成 SceneEvent,推给当前会话 ----

func (s *Server) SyncState(observerID int64, states []scene.PlayerState) {
	players := make([]*pb.PlayerState, 0, len(states))
	for _, st := range states {
		players = append(players, &pb.PlayerState{PlayerId: st.ID, X: st.X, Y: st.Y})
	}
	s.emit(observerID, msgStateSync, &pb.StateSync{Players: players})
}

func (s *Server) NotifyEnter(observerID, subjectID int64, x, y float32) {
	s.emit(observerID, msgPlayerEnter, &pb.PlayerEnter{PlayerId: subjectID, X: x, Y: y})
}

func (s *Server) NotifyLeave(observerID, subjectID int64) {
	s.emit(observerID, msgPlayerLeave, &pb.PlayerLeave{PlayerId: subjectID})
}

// SyncAll 用于上帝视角观战,分布式模式 5a 暂不支持。
func (s *Server) SyncAll(states []scene.PlayerState) {}

// emit 把一条下行事件编码后塞进当前会话的通道(无会话或积压则丢弃)。
func (s *Server) emit(target int64, msgType uint32, msg proto.Message) {
	s.mu.Lock()
	out := s.out
	s.mu.Unlock()
	if out == nil {
		return
	}
	payload, err := proto.Marshal(msg)
	if err != nil {
		log.Printf("scene: marshal: %v", err)
		return
	}
	select {
	case out <- &pb.SceneEvent{TargetPlayerId: target, MsgType: msgType, Payload: payload}:
	default: // 下行积压,丢弃(状态同步容忍丢帧)
	}
}

// Session 是双向流:读上行 GatewayEvent 驱动场景,把下行 SceneEvent 发回网关。
func (s *Server) Session(stream pb.SceneService_SessionServer) error {
	out := make(chan *pb.SceneEvent, 1024)
	done := make(chan struct{})
	s.mu.Lock()
	s.out = out
	s.mu.Unlock()
	log.Printf("scene: gateway session opened")

	// 下行 goroutine:把场景产生的事件通过流发回网关。
	go func() {
		for {
			select {
			case ev := <-out:
				if err := stream.Send(ev); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	// 上行循环:读网关事件,驱动场景。
	for {
		ev, err := stream.Recv()
		if err != nil {
			s.mu.Lock()
			s.out = nil // 先停止 emit 使用本通道(避免发往已结束的会话)
			s.mu.Unlock()
			close(done)
			log.Printf("scene: gateway session closed: %v", err)
			return err
		}
		switch e := ev.GetEvent().(type) {
		case *pb.GatewayEvent_Join:
			s.scene.Join(e.Join.GetPlayerId(), e.Join.GetX(), e.Join.GetY())
		case *pb.GatewayEvent_Move:
			s.scene.Move(e.Move.GetPlayerId(), e.Move.GetX(), e.Move.GetY())
		case *pb.GatewayEvent_Leave:
			s.scene.Leave(e.Leave.GetPlayerId())
		}
	}
}
