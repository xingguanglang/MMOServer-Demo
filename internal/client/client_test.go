package client

import (
	"net"
	"testing"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/gateway"
)

// startServer 在随机端口起一个真实网关服务器,返回地址和清理函数。
func startServer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	go gateway.NewServer(true).Serve(ln)
	return ln.Addr().String(), func() { ln.Close() }
}

// TestClientSeesOtherPlayerMove:端到端验证客户端网络层——
// A 移动后,B 的本地世界模型应通过状态同步看到 A 的新位置。
func TestClientSeesOtherPlayerMove(t *testing.T) {
	addr, cleanup := startServer(t)
	defer cleanup()

	a, err := Dial(addr)
	if err != nil {
		t.Fatalf("A dial: %v", err)
	}
	defer a.Close()
	if err := a.Login("alice"); err != nil {
		t.Fatalf("A login: %v", err)
	}
	go a.Run()

	b, err := Dial(addr)
	if err != nil {
		t.Fatalf("B dial: %v", err)
	}
	defer b.Close()
	if err := b.Login("bob"); err != nil {
		t.Fatalf("B login: %v", err)
	}
	go b.Run()

	// A 移动到一个仍在 B 视野内的位置。
	if err := a.Move(12.5, 20.0); err != nil {
		t.Fatalf("A move: %v", err)
	}

	// B 的世界模型应在 10Hz 状态同步后看到 A 的新坐标。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range b.Players() {
			if p.ID == a.PlayerID() && p.X == 12.5 && p.Y == 20.0 {
				return // 通过
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("B 未能在超时内通过状态同步看到 A 的新位置")
}

// TestClientSeesLeaveOnDisconnect:A 断线后,B 的世界模型应把 A 移除。
func TestClientSeesLeaveOnDisconnect(t *testing.T) {
	addr, cleanup := startServer(t)
	defer cleanup()

	a, err := Dial(addr)
	if err != nil {
		t.Fatalf("A dial: %v", err)
	}
	if err := a.Login("alice"); err != nil {
		t.Fatalf("A login: %v", err)
	}
	go a.Run()

	b, err := Dial(addr)
	if err != nil {
		t.Fatalf("B dial: %v", err)
	}
	defer b.Close()
	if err := b.Login("bob"); err != nil {
		t.Fatalf("B login: %v", err)
	}
	go b.Run()

	// 先等 B 看到 A。
	aID := a.PlayerID()
	if !waitFor(2*time.Second, func() bool { return hasPlayer(b, aID) }) {
		t.Fatal("B 应先看到 A")
	}

	// A 断线,B 应收到离开视野、把 A 移除。
	a.Close()
	if !waitFor(2*time.Second, func() bool { return !hasPlayer(b, aID) }) {
		t.Fatal("A 断线后,B 应把 A 从世界模型移除")
	}
}

// TestSpectatorSeesAllPlayers:观战者(上帝视角)应看到全场玩家,即使他们彼此不在 AOI 视野内。
func TestSpectatorSeesAllPlayers(t *testing.T) {
	addr, cleanup := startServer(t)
	defer cleanup()

	a, err := Dial(addr)
	if err != nil {
		t.Fatalf("A dial: %v", err)
	}
	defer a.Close()
	if err := a.Login("alice"); err != nil {
		t.Fatalf("A login: %v", err)
	}
	go a.Run()

	b, err := Dial(addr)
	if err != nil {
		t.Fatalf("B dial: %v", err)
	}
	defer b.Close()
	if err := b.Login("bob"); err != nil {
		t.Fatalf("B login: %v", err)
	}
	go b.Run()

	// 把两人移到地图对角,确保彼此都不在对方 AOI 视野内。
	a.Move(10, 10)
	b.Move(240, 240)

	// 观战者连接。
	spec, err := Dial(addr)
	if err != nil {
		t.Fatalf("spectator dial: %v", err)
	}
	defer spec.Close()
	if err := spec.Spectate(); err != nil {
		t.Fatalf("spectate: %v", err)
	}
	go spec.Run()

	// 观战者应看到全部两个玩家(不过 AOI)。
	if !waitFor(2*time.Second, func() bool {
		return hasPlayer(spec, a.PlayerID()) && hasPlayer(spec, b.PlayerID())
	}) {
		t.Fatal("观战者应看到全场所有玩家(不受 AOI 限制)")
	}
}

// TestPlayerReceivesMinimap:普通玩家应收到低频全场快照(小地图),里面至少有自己。
func TestPlayerReceivesMinimap(t *testing.T) {
	addr, cleanup := startServer(t)
	defer cleanup()

	a, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer a.Close()
	if err := a.Login("alice"); err != nil {
		t.Fatalf("login: %v", err)
	}
	go a.Run()
	a.Move(100, 100)

	if !waitFor(2*time.Second, func() bool { return len(a.Minimap()) >= 1 }) {
		t.Fatal("玩家应收到小地图全场快照(至少含自己)")
	}
}

func hasPlayer(c *Client, id int64) bool {
	for _, p := range c.Players() {
		if p.ID == id {
			return true
		}
	}
	return false
}

func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
