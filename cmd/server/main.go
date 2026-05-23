package main

import (
	"flag"
	"log"
	"net"

	"github.com/xingguanglang/MMOServer-Demo/internal/gateway"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	aoi := flag.Bool("aoi", true, "enable AOI; set -aoi=false to broadcast to everyone (perf comparison)")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s failed: %v", *addr, err)
	}
	log.Printf("server listening on %s (aoi=%v)", *addr, *aoi)

	srv := gateway.NewServer(*aoi)
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
