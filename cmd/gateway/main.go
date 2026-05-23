// Command gateway is the distributed gateway: it accepts player TCP
// connections and forwards to the scene process over gRPC.
package main

import (
	"flag"
	"log"
	"net"

	"github.com/xingguanglang/MMOServer-Demo/internal/gwserver"
)

func main() {
	addr := flag.String("addr", ":9000", "player-facing TCP listen address")
	sceneAddr := flag.String("scene", "127.0.0.1:9100", "scene server gRPC address")
	flag.Parse()

	srv, err := gwserver.New(*sceneAddr)
	if err != nil {
		log.Fatalf("connect scene %s failed: %v", *sceneAddr, err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s failed: %v", *addr, err)
	}
	log.Printf("gateway listening on %s, scene at %s", *addr, *sceneAddr)
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("gateway stopped: %v", err)
	}
}
