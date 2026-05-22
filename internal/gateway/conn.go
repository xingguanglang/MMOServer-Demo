package gateway

import (
	"net"
	"sync"

	"github.com/xingguanglang/MMOServer-Demo/internal/protocol"
)

type Inbound struct {
	Conn    *Conn
	MsgType uint16
	Body    []byte
}
type Conn struct {
	id        uint64
	raw       net.Conn
	done      chan struct{} // 连接关闭的信号
	closeOnce sync.Once
	inbound   chan<- Inbound
	sendCh    chan []byte
}

func NewConn(id uint64, raw net.Conn, inbound chan<- Inbound) *Conn {
	return &Conn{
		id:      id,
		raw:     raw,
		done:    make(chan struct{}),
		inbound: inbound,
		sendCh:  make(chan []byte, 64), // 64 条消息的发送缓冲
	}
}
func (c *Conn) ID() uint64 {
	return c.id
}

// Done 返回连接的关闭信号 channel,供外部(如服务器)等待这条连接结束。
// 返回类型是 <-chan(只读 channel),外部只能从里面接收、不能 close,
// 保证关闭的控制权只在 Conn 自己手里。
func (c *Conn) Done() <-chan struct{} {
	return c.done
}
func (c *Conn) Start() {
	go c.readLoop()
	go c.writeloop()
}
func (c *Conn) Close() {
	c.closeOnce.Do(func() {
		close(c.done) // 先发关闭信号,让读写协程都退出
		c.raw.Close() // 关闭底层连接
	})
}
func (c *Conn) readLoop() {
	defer c.Close()
	for {
		msgType, body, err := protocol.ReadFrame(c.raw)
		if err != nil {
			return
		}
		select {
		case c.inbound <- Inbound{Conn: c, MsgType: msgType, Body: body}:
		case <-c.done:
			return
		}
	}
}
func (c *Conn) writeloop() {
	for {
		select {
		case data := <-c.sendCh:
			if _, err := c.raw.Write(data); err != nil {
				c.Close() // 发送失败就关闭连接
				return
			}
		case <-c.done:
			return
		}
	}
}
func (c *Conn) Send(data []byte) {
	select {
	case c.sendCh <- data:
	case <-c.done:
	}
}
