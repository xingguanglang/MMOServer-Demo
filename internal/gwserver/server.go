// Package gwserver is the distributed gateway: it owns TCP connections and
// talks to the scene process over a gRPC bidirectional stream. Player events
// (login/move/disconnect) go upstream; the scene's outbound messages come back
// downstream and are routed to the right connection.
//
// It reuses the low-level connection machinery (gateway.Conn) but, unlike the
// monolith gateway.Server, has no in-process scene — the scene lives in another
// process behind gRPC.
package gwserver

import (
	"context"
	"log"
	"net"
	"sync"

	"github.com/xingguanglang/MMOServer-Demo/internal/config"
	"github.com/xingguanglang/MMOServer-Demo/internal/gateway"
	"github.com/xingguanglang/MMOServer-Demo/internal/protocol"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// Server 是分布式网关。
type Server struct {
	inbound  chan gateway.Inbound
	upstream chan *pb.GatewayEvent             // 上行事件队列(串行发送,避免并发 Send 流)
	stream   grpc.BidiStreamingClient[pb.GatewayEvent, pb.SceneEvent]

	mu     sync.RWMutex
	conns  map[uint64]*gateway.Conn
	nextID uint64
}

// New 连接场景服(gRPC),开一条 Session 流,并启动收发/逻辑 goroutine。
func New(sceneAddr string) (*Server, error) {
	conn, err := grpc.NewClient(sceneAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	stream, err := pb.NewSceneServiceClient(conn).Session(context.Background())
	if err != nil {
		return nil, err
	}
	s := &Server{
		inbound:  make(chan gateway.Inbound, 1024),
		upstream: make(chan *pb.GatewayEvent, 1024),
		stream:   stream,
		conns:    make(map[uint64]*gateway.Conn),
	}
	go s.sendLoop() // 串行把上行事件发给场景
	go s.recvLoop() // 接收场景下行事件并路由到连接
	go s.logicLoop()
	return s, nil
}

// Serve 接受 TCP 连接(玩家客户端连这里)。
func (s *Server) Serve(ln net.Listener) error {
	for {
		raw, err := ln.Accept()
		if err != nil {
			return err
		}
		s.nextID++
		c := gateway.NewConn(s.nextID, raw, s.inbound)
		s.addConn(c)
		c.Start()
		log.Printf("gateway: conn %d connected", c.ID())

		go func(c *gateway.Conn) {
			<-c.Done()
			s.upstream <- &pb.GatewayEvent{Event: &pb.GatewayEvent_Leave{
				Leave: &pb.PlayerLeaveReq{PlayerId: int64(c.ID())},
			}}
			s.removeConn(c.ID())
			log.Printf("gateway: conn %d disconnected", c.ID())
		}(c)
	}
}

func (s *Server) addConn(c *gateway.Conn) {
	s.mu.Lock()
	s.conns[c.ID()] = c
	s.mu.Unlock()
}

func (s *Server) removeConn(id uint64) {
	s.mu.Lock()
	delete(s.conns, id)
	s.mu.Unlock()
}

func (s *Server) connByPlayer(playerID int64) *gateway.Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conns[uint64(playerID)]
}

// sendLoop 串行地把上行事件写进 gRPC 流(流不允许并发 Send)。
func (s *Server) sendLoop() {
	for ev := range s.upstream {
		if err := s.stream.Send(ev); err != nil {
			log.Printf("gateway: upstream send failed: %v", err)
			return
		}
	}
}

// recvLoop 读场景下行事件,按 target_player_id 路由到对应连接。
func (s *Server) recvLoop() {
	for {
		ev, err := s.stream.Recv()
		if err != nil {
			log.Printf("gateway: downstream recv ended: %v", err)
			return
		}
		c := s.connByPlayer(ev.GetTargetPlayerId())
		if c == nil {
			continue
		}
		packet, err := protocol.Encode(uint16(ev.GetMsgType()), ev.GetPayload())
		if err != nil {
			continue
		}
		c.Send(packet)
	}
}

func (s *Server) logicLoop() {
	for in := range s.inbound {
		switch in.MsgType {
		case gateway.MsgLoginReq:
			s.handleLogin(in)
		case gateway.MsgMoveReq:
			s.handleMove(in)
		default:
			log.Printf("gateway: conn %d unknown msg type %d", in.Conn.ID(), in.MsgType)
		}
	}
}

// handleLogin 在网关本地回 LoginResp(认证是网关职责),再让场景加入该玩家。
func (s *Server) handleLogin(in gateway.Inbound) {
	var req pb.LoginReq
	if err := proto.Unmarshal(in.Body, &req); err != nil {
		return
	}
	playerID := int64(in.Conn.ID())
	body, err := proto.Marshal(&pb.LoginResp{PlayerId: playerID, Success: true, Message: "welcome " + req.GetUsername()})
	if err == nil {
		if packet, e := protocol.Encode(gateway.MsgLoginResp, body); e == nil {
			in.Conn.Send(packet)
		}
	}
	s.upstream <- &pb.GatewayEvent{Event: &pb.GatewayEvent_Join{
		Join: &pb.PlayerJoin{PlayerId: playerID, X: config.SpawnX, Y: config.SpawnY},
	}}
}

// handleMove 把移动转发给场景。
func (s *Server) handleMove(in gateway.Inbound) {
	var req pb.MoveReq
	if err := proto.Unmarshal(in.Body, &req); err != nil {
		return
	}
	s.upstream <- &pb.GatewayEvent{Event: &pb.GatewayEvent_Move{
		Move: &pb.PlayerMoveReq{PlayerId: int64(in.Conn.ID()), X: req.GetX(), Y: req.GetY()},
	}}
}
