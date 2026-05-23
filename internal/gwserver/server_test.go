package gwserver_test

import (
	"net"
	"testing"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/client"
	"github.com/xingguanglang/MMOServer-Demo/internal/gwserver"
	"github.com/xingguanglang/MMOServer-Demo/internal/sceneserver"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/grpc"
)

// startScene 在随机端口起一个场景 gRPC 服务,返回地址和清理函数。
func startScene(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("scene listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterSceneServiceServer(gs, sceneserver.New())
	go gs.Serve(lis)
	return lis.Addr().String(), gs.Stop
}

// startGateway 在随机端口起分布式网关(连到 sceneAddr),返回玩家可连的 TCP 地址。
func startGateway(t *testing.T, sceneAddr string) (addr string, cleanup func()) {
	t.Helper()
	srv, err := gwserver.New(sceneAddr)
	if err != nil {
		t.Fatalf("gateway new: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("gateway listen: %v", err)
	}
	go srv.Serve(ln)
	return ln.Addr().String(), func() { ln.Close() }
}

// TestDistributedMoveSync 端到端验证拆分:玩家移动经
// TCP→网关→gRPC→场景→gRPC→网关→TCP 一圈后,另一个玩家能看到。
func TestDistributedMoveSync(t *testing.T) {
	sceneAddr, stopScene := startScene(t)
	defer stopScene()
	gwAddr, stopGw := startGateway(t, sceneAddr)
	defer stopGw()

	a, err := client.Dial(gwAddr)
	if err != nil {
		t.Fatalf("A dial: %v", err)
	}
	defer a.Close()
	if err := a.Login("alice"); err != nil {
		t.Fatalf("A login: %v", err)
	}
	go a.Run()

	b, err := client.Dial(gwAddr)
	if err != nil {
		t.Fatalf("B dial: %v", err)
	}
	defer b.Close()
	if err := b.Login("bob"); err != nil {
		t.Fatalf("B login: %v", err)
	}
	go b.Run()

	if err := a.Move(12.5, 20.0); err != nil {
		t.Fatalf("A move: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range b.Players() {
			if p.ID == a.PlayerID() && p.X == 12.5 && p.Y == 20.0 {
				return // 通过:跨进程一圈后 B 看到了 A 的新位置
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("B 未能通过分布式链路看到 A 的新位置")
}
