package gateway

import (
	"log"
	"net"
	"sync"

	"github.com/xingguanglang/MMOServer-Demo/internal/protocol"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/protobuf/proto"
)

// 消息类型编号。客户端和服务器必须共用同一套,否则路由会错。
// 阶段 1 先放在这里,后面如果客户端也用 Go,可以抽到一个共享包。
const (
	MsgLoginReq      uint16 = 1
	MsgLoginResp     uint16 = 2
	MsgMoveReq       uint16 = 3
	MsgMoveBroadcast uint16 = 4
)

// Server 是阶段 1 的单体服务器:网关 + 登录 + 广播都在一个进程里。
type Server struct {
	inbound chan Inbound // 所有连接收到的消息都汇聚到这里

	// 连接表。被 accept goroutine(写)和逻辑 goroutine(读)共享,所以用读写锁保护。
	mu     sync.RWMutex
	conns  map[uint64]*Conn
	nextID uint64
}

// NewServer 创建服务器。
func NewServer() *Server {
	return &Server{
		inbound: make(chan Inbound, 1024), // 缓冲大一些,吸收突发流量
		conns:   make(map[uint64]*Conn),
	}
}

// Serve 在给定的 listener 上开始服务。设计成接收 net.Listener 而不是地址字符串,
// 是为了可测试:测试里可以传一个监听随机端口的 listener。
func (s *Server) Serve(ln net.Listener) error {
	// 启动全局唯一的逻辑 goroutine,串行处理所有消息。
	go s.logicLoop()

	for {
		raw, err := ln.Accept()
		if err != nil {
			return err // listener 被关闭时 Accept 会出错,正常退出
		}

		s.nextID++ // accept 循环是单 goroutine,这里自增不用加锁
		c := NewConn(s.nextID, raw, s.inbound)
		s.addConn(c)
		c.Start()
		log.Printf("conn %d connected", c.ID())

		// 为每条连接起一个"看门狗":等它结束后,把它从连接表移除,避免泄漏。
		go func(c *Conn) {
			<-c.Done()
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
	s.mu.Unlock()
}

// logicLoop 是全局唯一的逻辑处理 goroutine,串行消费 inbound 通道。
// 因为只有它一个 goroutine 在跑业务逻辑,后面的游戏状态可以做到基本无锁。
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
	default:
		log.Printf("conn %d: unknown msg type %d", in.Conn.ID(), in.MsgType)
	}
}

// handleLogin 处理登录:阶段 1 极简,直接用连接 ID 当玩家 ID,登录必成功。
func (s *Server) handleLogin(in Inbound) {
	var req pb.LoginReq
	if err := proto.Unmarshal(in.Body, &req); err != nil {
		log.Printf("conn %d: bad LoginReq: %v", in.Conn.ID(), err)
		return
	}
	resp := &pb.LoginResp{
		PlayerId: int64(in.Conn.ID()),
		Success:  true,
		Message:  "welcome " + req.GetUsername(),
	}
	s.sendTo(in.Conn, MsgLoginResp, resp)
	log.Printf("conn %d logged in as %q", in.Conn.ID(), req.GetUsername())
}

// handleMove 处理移动:把这个玩家的新坐标广播给其他所有人。
func (s *Server) handleMove(in Inbound) {
	var req pb.MoveReq
	if err := proto.Unmarshal(in.Body, &req); err != nil {
		log.Printf("conn %d: bad MoveReq: %v", in.Conn.ID(), err)
		return
	}
	bc := &pb.MoveBroadcast{
		PlayerId: int64(in.Conn.ID()),
		X:        req.GetX(),
		Y:        req.GetY(),
	}
	s.broadcastExcept(in.Conn.ID(), MsgMoveBroadcast, bc)
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

// broadcastExcept 把消息广播给除 exceptID 外的所有连接。
func (s *Server) broadcastExcept(exceptID uint64, msgType uint16, msg proto.Message) {
	packet, err := encodeMessage(msgType, msg)
	if err != nil {
		log.Printf("encode error: %v", err)
		return
	}

	// 先在锁内拍一张连接快照,再到锁外逐个发送,尽量缩短持锁时间,
	// 避免某个慢客户端的 Send 卡住整张连接表。
	s.mu.RLock()
	targets := make([]*Conn, 0, len(s.conns))
	for id, c := range s.conns {
		if id != exceptID {
			targets = append(targets, c)
		}
	}
	s.mu.RUnlock()

	for _, c := range targets {
		c.Send(packet)
	}
}

// encodeMessage 把 protobuf 消息序列化,再套上我们的 [长度][类型][体] 协议帧。
func encodeMessage(msgType uint16, msg proto.Message) ([]byte, error) {
	body, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return protocol.Encode(msgType, body)
}
