// Package client 是连接 MMO 服务器的客户端网络层:连接、登录、收发消息,
// 并维护一份"我能看到的其他玩家"的本地世界模型。
// 它不依赖任何图形库,可被 ebiten 可视化客户端和压测机器人共用。
package client

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/xingguanglang/MMOServer-Demo/internal/protocol"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/protobuf/proto"
)

// 消息类型编号,直接取自 proto 的 MsgId 枚举,保证和服务器同源、不会写错。
const (
	msgLoginReq    = uint16(pb.MsgId_MSG_LOGIN_REQ)
	msgLoginResp   = uint16(pb.MsgId_MSG_LOGIN_RESP)
	msgMoveReq     = uint16(pb.MsgId_MSG_MOVE_REQ)
	msgPlayerEnter = uint16(pb.MsgId_MSG_PLAYER_ENTER)
	msgPlayerLeave = uint16(pb.MsgId_MSG_PLAYER_LEAVE)
	msgStateSync   = uint16(pb.MsgId_MSG_STATE_SYNC)
)

// RemotePlayer 是客户端视野里的一个其他玩家,位置取自服务器最近一次状态同步。
type RemotePlayer struct {
	ID   int64
	X, Y float32
}

// Client 维护与服务器的连接,以及一份本地世界模型(视野内其他玩家)。
// 世界模型被接收 goroutine(写)和渲染/逻辑(读)共享,因此用读写锁保护。
type Client struct {
	conn     net.Conn
	playerID int64

	mu      sync.RWMutex
	players map[int64]*RemotePlayer
	selfX   float32
	selfY   float32

	recv atomic.Uint64 // 收到的消息帧计数,用于压测统计吞吐
}

// Dial 连接服务器。
func Dial(addr string) (*Client, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:    conn,
		players: make(map[int64]*RemotePlayer),
	}, nil
}

// Login 发送登录请求并同步等待响应,成功后记录服务器分配的玩家 ID。
func (c *Client) Login(username string) error {
	if err := c.send(msgLoginReq, &pb.LoginReq{Username: username}); err != nil {
		return err
	}
	msgType, body, err := protocol.ReadFrame(c.conn)
	if err != nil {
		return err
	}
	if msgType != msgLoginResp {
		return fmt.Errorf("expected login resp(%d), got %d", msgLoginResp, msgType)
	}
	var resp pb.LoginResp
	if err := proto.Unmarshal(body, &resp); err != nil {
		return err
	}
	if !resp.GetSuccess() {
		return fmt.Errorf("login failed: %s", resp.GetMessage())
	}
	c.playerID = resp.GetPlayerId()
	return nil
}

// Run 是接收循环:不停读帧、更新本地世界模型。阻塞,通常放在 goroutine 里跑。
// 连接断开或出错时返回。
func (c *Client) Run() error {
	for {
		msgType, body, err := protocol.ReadFrame(c.conn)
		if err != nil {
			return err
		}
		c.recv.Add(1)
		c.handle(msgType, body)
	}
}

// ReceivedCount 返回累计收到的消息帧数(并发安全)。
func (c *Client) ReceivedCount() uint64 { return c.recv.Load() }

func (c *Client) handle(msgType uint16, body []byte) {
	switch msgType {
	case msgPlayerEnter:
		var m pb.PlayerEnter
		if proto.Unmarshal(body, &m) == nil {
			c.upsert(m.GetPlayerId(), m.GetX(), m.GetY())
		}
	case msgPlayerLeave:
		var m pb.PlayerLeave
		if proto.Unmarshal(body, &m) == nil {
			c.remove(m.GetPlayerId())
		}
	case msgStateSync:
		var m pb.StateSync
		if proto.Unmarshal(body, &m) == nil {
			c.mu.Lock()
			for _, st := range m.GetPlayers() {
				p := c.players[st.GetPlayerId()]
				if p == nil { // 容错:即便漏了 Enter,也按状态同步补上
					p = &RemotePlayer{ID: st.GetPlayerId()}
					c.players[st.GetPlayerId()] = p
				}
				p.X, p.Y = st.GetX(), st.GetY()
			}
			c.mu.Unlock()
		}
	}
}

func (c *Client) upsert(id int64, x, y float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.players[id]
	if p == nil {
		p = &RemotePlayer{ID: id}
		c.players[id] = p
	}
	p.X, p.Y = x, y
}

func (c *Client) remove(id int64) {
	c.mu.Lock()
	delete(c.players, id)
	c.mu.Unlock()
}

// Move 上报自己的新位置,并更新本地的"自己"坐标。
func (c *Client) Move(x, y float32) error {
	c.mu.Lock()
	c.selfX, c.selfY = x, y
	c.mu.Unlock()
	return c.send(msgMoveReq, &pb.MoveReq{X: x, Y: y})
}

// Players 返回当前视野内其他玩家的快照副本(供渲染读取,不会与接收 goroutine 抢数据)。
func (c *Client) Players() []RemotePlayer {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]RemotePlayer, 0, len(c.players))
	for _, p := range c.players {
		out = append(out, *p)
	}
	return out
}

// Self 返回自己当前坐标。
func (c *Client) Self() (x, y float32) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.selfX, c.selfY
}

// PlayerID 返回服务器分配的玩家 ID(登录后有效)。
func (c *Client) PlayerID() int64 { return c.playerID }

// Close 关闭连接。
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) send(msgType uint16, msg proto.Message) error {
	body, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	packet, err := protocol.Encode(msgType, body)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(packet)
	return err
}
