package gateway

import (
	"net"
	"testing"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/protocol"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/protobuf/proto"
)

// startTestServer 在随机端口起一个服务器,返回它的地址和清理函数。
func startTestServer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	// 端口 0 让系统自动分配空闲端口,测试之间不会撞端口。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	srv := NewServer(true)
	go srv.Serve(ln) // Serve 阻塞,放后台跑
	return ln.Addr().String(), func() { ln.Close() }
}

// dialAndLogin 连上服务器、发登录请求、读回登录响应,返回连接和分配到的玩家 ID。
func dialAndLogin(t *testing.T, addr, username string) (net.Conn, int64) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	body, _ := proto.Marshal(&pb.LoginReq{Username: username})
	packet, _ := protocol.Encode(MsgLoginReq, body)
	if _, err := conn.Write(packet); err != nil {
		t.Fatalf("write login failed: %v", err)
	}

	// 登录响应在 Join 之前发出,所以第一帧一定是 LoginResp。
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
	conn.SetReadDeadline(time.Time{})
	return conn, resp.GetPlayerId()
}

// sendMove 让客户端上报一次移动。
func sendMove(t *testing.T, conn net.Conn, x, y float32) {
	t.Helper()
	body, _ := proto.Marshal(&pb.MoveReq{X: x, Y: y})
	packet, _ := protocol.Encode(MsgMoveReq, body)
	if _, err := conn.Write(packet); err != nil {
		t.Fatalf("write move failed: %v", err)
	}
}

// readUntil 持续读帧,直到读到 wantType 的消息(返回其 body),或超时失败。
// 用来跳过途中夹带的其他事件(比如登录时的 PlayerEnter)。
func readUntil(t *testing.T, conn net.Conn, wantType uint16, timeout time.Duration) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	for {
		msgType, body, err := protocol.ReadFrame(conn)
		if err != nil {
			t.Fatalf("等待消息类型 %d 时读取失败: %v", wantType, err)
		}
		if msgType == wantType {
			return body
		}
	}
}

// TestPlayerEnterOnLogin:两个玩家在同一格出生,后进的玩家应让先进的收到"进入视野"。
func TestPlayerEnterOnLogin(t *testing.T) {
	addr, cleanup := startTestServer(t)
	defer cleanup()

	connA, _ := dialAndLogin(t, addr, "alice")
	defer connA.Close()
	connB, idB := dialAndLogin(t, addr, "bob")
	defer connB.Close()

	// B 进场后,A 应收到 B 进入视野。
	body := readUntil(t, connA, MsgPlayerEnter, 2*time.Second)
	var pe pb.PlayerEnter
	if err := proto.Unmarshal(body, &pe); err != nil {
		t.Fatalf("unmarshal PlayerEnter failed: %v", err)
	}
	if pe.GetPlayerId() != idB {
		t.Errorf("A 收到的 PlayerEnter 主体: got %d, want %d (B)", pe.GetPlayerId(), idB)
	}
}

// TestStateSyncToNearbyPlayer:同格内的玩家移动后,视野内的另一个玩家会在 10Hz 状态同步里
// 看到它的新坐标。
func TestStateSyncToNearbyPlayer(t *testing.T) {
	addr, cleanup := startTestServer(t)
	defer cleanup()

	connA, idA := dialAndLogin(t, addr, "alice")
	defer connA.Close()
	connB, _ := dialAndLogin(t, addr, "bob")
	defer connB.Close()

	sendMove(t, connA, 12.5, 20.0) // 仍在 grid0,B 在视野内

	// 持续读 StateSync,直到看到 A 出现在新坐标(B 视野内会周期性收到 A 的状态)。
	connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	defer connB.SetReadDeadline(time.Time{})
	for {
		msgType, body, err := protocol.ReadFrame(connB)
		if err != nil {
			t.Fatalf("等待含 A 新坐标的 StateSync 超时: %v", err)
		}
		if msgType != MsgStateSync {
			continue // 跳过登录时的 PlayerEnter 等其他消息
		}
		var ss pb.StateSync
		if err := proto.Unmarshal(body, &ss); err != nil {
			t.Fatalf("unmarshal StateSync failed: %v", err)
		}
		for _, st := range ss.GetPlayers() {
			if st.GetPlayerId() == idA && st.GetX() == 12.5 && st.GetY() == 20.0 {
				return // 找到 A 的新坐标,通过
			}
		}
	}
}

// TestPlayerLeaveOnMoveAway:玩家移动到远处、离开对方视野,对方应收到"离开视野"。
func TestPlayerLeaveOnMoveAway(t *testing.T) {
	addr, cleanup := startTestServer(t)
	defer cleanup()

	connA, idA := dialAndLogin(t, addr, "alice")
	defer connA.Close()
	connB, _ := dialAndLogin(t, addr, "bob")
	defer connB.Close()

	sendMove(t, connA, 200, 200) // 从 grid0 远离到 grid54,离开 B 的视野

	body := readUntil(t, connB, MsgPlayerLeave, 2*time.Second)
	var pl pb.PlayerLeave
	if err := proto.Unmarshal(body, &pl); err != nil {
		t.Fatalf("unmarshal PlayerLeave failed: %v", err)
	}
	if pl.GetPlayerId() != idA {
		t.Errorf("离开视野主体: got %d, want %d (A)", pl.GetPlayerId(), idA)
	}
}

// TestPlayerLeaveOnDisconnect:玩家断线,原本能看到它的玩家应收到"离开视野"。
func TestPlayerLeaveOnDisconnect(t *testing.T) {
	addr, cleanup := startTestServer(t)
	defer cleanup()

	connA, idA := dialAndLogin(t, addr, "alice")
	connB, _ := dialAndLogin(t, addr, "bob")
	defer connB.Close()

	connA.Close() // A 断线

	body := readUntil(t, connB, MsgPlayerLeave, 2*time.Second)
	var pl pb.PlayerLeave
	if err := proto.Unmarshal(body, &pl); err != nil {
		t.Fatalf("unmarshal PlayerLeave failed: %v", err)
	}
	if pl.GetPlayerId() != idA {
		t.Errorf("断线离开主体: got %d, want %d (A)", pl.GetPlayerId(), idA)
	}
}

// TestSoloPlayerGetsEmptyAOISync:场上只有自己时移动,仍会收到一条「空」AOI 状态同步。
// 这是刻意为之的权威对账:即使对应的 PlayerLeave 在过载丢帧时丢失,客户端也能靠这条
// 空快照把视野清空、剔除「鬼影」。
func TestSoloPlayerGetsEmptyAOISync(t *testing.T) {
	addr, cleanup := startTestServer(t)
	defer cleanup()

	connA, _ := dialAndLogin(t, addr, "alice")
	defer connA.Close()

	sendMove(t, connA, 1, 1)

	// 应收到一条 AOI StateSync,且其中不含任何其他玩家(空快照)。
	body := readUntil(t, connA, MsgStateSync, 2*time.Second)
	var ss pb.StateSync
	if err := proto.Unmarshal(body, &ss); err != nil {
		t.Fatalf("unmarshal StateSync failed: %v", err)
	}
	if len(ss.GetPlayers()) != 0 {
		t.Errorf("场上只有自己,AOI 状态同步应为空,实际含 %d 人", len(ss.GetPlayers()))
	}
}
