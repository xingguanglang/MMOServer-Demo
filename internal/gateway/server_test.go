package gateway

import (
	"net"
	"testing"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/protocol"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/protobuf/proto"
)

// startTestServer 在随机端口起一个服务器,返回它的地址和一个清理函数。
func startTestServer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	// 监听 127.0.0.1:0 —— 端口 0 让系统自动分配一个空闲端口,测试之间不会撞端口。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	srv := NewServer()
	go srv.Serve(ln) // Serve 会阻塞,放到后台 goroutine 跑
	return ln.Addr().String(), func() { ln.Close() }
}

// dialAndLogin 连上服务器、发登录请求、读回登录响应,返回连接和分配到的玩家 ID。
func dialAndLogin(t *testing.T, addr, username string) (net.Conn, int64) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	// 发送登录请求。
	body, _ := proto.Marshal(&pb.LoginReq{Username: username})
	packet, _ := protocol.Encode(MsgLoginReq, body)
	if _, err := conn.Write(packet); err != nil {
		t.Fatalf("write login failed: %v", err)
	}

	// 读回登录响应。
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, respBody, err := protocol.ReadFrame(conn)
	if err != nil {
		t.Fatalf("read login resp failed: %v", err)
	}
	if msgType != MsgLoginResp {
		t.Fatalf("expected MsgLoginResp(%d), got %d", MsgLoginResp, msgType)
	}
	var resp pb.LoginResp
	if err := proto.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("unmarshal login resp failed: %v", err)
	}
	if !resp.GetSuccess() {
		t.Fatalf("login not successful: %s", resp.GetMessage())
	}
	conn.SetReadDeadline(time.Time{}) // 清除读超时
	return conn, resp.GetPlayerId()
}

// TestMoveBroadcast 验证阶段 1 目标:一个玩家移动,另一个玩家能收到广播。
func TestMoveBroadcast(t *testing.T) {
	addr, cleanup := startTestServer(t)
	defer cleanup()

	// 两个客户端分别连上并登录。
	connA, idA := dialAndLogin(t, addr, "alice")
	defer connA.Close()
	connB, _ := dialAndLogin(t, addr, "bob")
	defer connB.Close()

	// A 发送一次移动。
	moveBody, _ := proto.Marshal(&pb.MoveReq{X: 12.5, Y: -3.0})
	movePacket, _ := protocol.Encode(MsgMoveReq, moveBody)
	if _, err := connA.Write(movePacket); err != nil {
		t.Fatalf("A write move failed: %v", err)
	}

	// B 应该收到一条 A 的移动广播。
	connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, body, err := protocol.ReadFrame(connB)
	if err != nil {
		t.Fatalf("B read broadcast failed: %v", err)
	}
	if msgType != MsgMoveBroadcast {
		t.Fatalf("expected MsgMoveBroadcast(%d), got %d", MsgMoveBroadcast, msgType)
	}

	var bc pb.MoveBroadcast
	if err := proto.Unmarshal(body, &bc); err != nil {
		t.Fatalf("unmarshal broadcast failed: %v", err)
	}
	if bc.GetPlayerId() != idA {
		t.Errorf("broadcast playerId: got %d, want %d (A)", bc.GetPlayerId(), idA)
	}
	if bc.GetX() != 12.5 || bc.GetY() != -3.0 {
		t.Errorf("broadcast coords: got (%v, %v), want (12.5, -3.0)", bc.GetX(), bc.GetY())
	}
}

// TestSenderDoesNotReceiveOwnBroadcast 验证:移动广播不会发回给移动者自己。
func TestSenderDoesNotReceiveOwnBroadcast(t *testing.T) {
	addr, cleanup := startTestServer(t)
	defer cleanup()

	connA, _ := dialAndLogin(t, addr, "alice")
	defer connA.Close()

	moveBody, _ := proto.Marshal(&pb.MoveReq{X: 1, Y: 1})
	movePacket, _ := protocol.Encode(MsgMoveReq, moveBody)
	connA.Write(movePacket)

	// A 自己不该收到任何东西,读超时即视为通过。
	connA.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err := protocol.ReadFrame(connA)
	if err == nil {
		t.Fatal("sender unexpectedly received its own broadcast")
	}
}
