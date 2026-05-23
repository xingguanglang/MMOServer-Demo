package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xingguanglang/MMOServer-Demo/internal/api"
	"github.com/xingguanglang/MMOServer-Demo/internal/config"
	"github.com/xingguanglang/MMOServer-Demo/internal/gateway"
)

func main() {
	addr := flag.String("addr", config.GameAddr, "game server (TCP) listen address")
	httpAddr := flag.String("http", config.HTTPAddr, "HTTP control API listen address")
	aoi := flag.Bool("aoi", true, "enable AOI; set -aoi=false to broadcast to everyone (perf comparison)")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s failed: %v", *addr, err)
	}
	log.Printf("game server listening on %s (aoi=%v)", *addr, *aoi)

	srv := gateway.NewServer(*aoi)

	// HTTP 控制 API + 管理台:用真实客户端连本机游戏服务器来生成玩家、查询全场位置、暴露指标。
	apiSrv := api.NewServer(dialable(*addr),
		func() []api.PlayerPos {
			snap := srv.Snapshot()
			out := make([]api.PlayerPos, 0, len(snap))
			for _, p := range snap {
				out = append(out, api.PlayerPos{ID: p.ID, X: p.X, Y: p.Y})
			}
			return out
		},
		func() api.Metrics {
			tickHz, aoiHz, allHz := srv.Rates()
			return api.Metrics{
				Players:     len(srv.Snapshot()),
				Connections: srv.ConnCount(),
				SentBytes:   srv.SentBytes(),
				AOIEnabled:  srv.AOIEnabled(),
				TickHz:      tickHz,
				AOIHz:       aoiHz,
				AllHz:       allHz,
			}
		})
	// 管理台"打开客户端/观战"按钮:在本机启动 ebiten 客户端窗口(与本 exe 同目录的 client 二进制)。
	apiSrv.SetLauncher(launchClient(dialable(*addr)))
	// 管理台"改帧率":运行时调场景的 tick / AOI / 全场频率。
	apiSrv.SetRateSetter(srv.SetRates)
	// 管理台"AOI 开关":运行时切 AOI(关 = 全场广播)。
	apiSrv.SetAOISetter(srv.SetAOIEnabled)
	// 管理台"清空玩家":断开所有连接。
	apiSrv.SetClearer(srv.DisconnectAll)

	go func() {
		log.Printf("HTTP API listening on %s", *httpAddr)
		if err := http.ListenAndServe(*httpAddr, apiSrv.Handler()); err != nil {
			log.Printf("http api stopped: %v", err)
		}
	}()

	if err := srv.Serve(ln); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

// dialable 把监听地址(可能是 ":9000")转成可拨号地址("127.0.0.1:9000")。
func dialable(listenAddr string) string {
	if strings.HasPrefix(listenAddr, ":") {
		return "127.0.0.1" + listenAddr
	}
	return listenAddr
}

// launchClient 返回一个启动器:exec 与本 server 同目录的客户端二进制(普通或观战),
// 连到 gameAddr。需要先 `go build -o bin/ ./cmd/...`(server 和 client 都在 bin/)。
func launchClient(gameAddr string) func(bool) error {
	return func(spectate bool) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		name := "client"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(filepath.Dir(exe), name)
		args := []string{"-addr", gameAddr}
		if spectate {
			args = append(args, "-spectate")
		}
		return exec.Command(path, args...).Start()
	}
}
