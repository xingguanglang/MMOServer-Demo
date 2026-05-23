package main

import (
	"flag"
	"log"
	"net"
	"net/http"
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

	// HTTP 控制 API:用真实客户端连本机游戏服务器来生成玩家,并查询全场位置。
	apiSrv := api.NewServer(dialable(*addr), func() []api.PlayerPos {
		snap := srv.Snapshot()
		out := make([]api.PlayerPos, 0, len(snap))
		for _, p := range snap {
			out = append(out, api.PlayerPos{ID: p.ID, X: p.X, Y: p.Y})
		}
		return out
	})
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
