package main

import (
	"log"
	"net"

	"github.com/xingguanglang/MMOServer-Demo/internal/gateway"
)

func main() {
	const addr = ":9000"

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen %s failed: %v", addr, err)
	}
	log.Printf("server listening on %s", addr)

	srv := gateway.NewServer()
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
