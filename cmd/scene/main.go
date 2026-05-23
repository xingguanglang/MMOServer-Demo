// Command scene runs the scene server: a gRPC SceneService backed by the
// tick loop + AOI. The gateway connects to it over gRPC.
package main

import (
	"flag"
	"log"
	"net"

	"github.com/xingguanglang/MMOServer-Demo/internal/config"
	"github.com/xingguanglang/MMOServer-Demo/internal/sceneserver"
	"github.com/xingguanglang/MMOServer-Demo/pkg/pb"
	"google.golang.org/grpc"
)

func main() {
	addr := flag.String("addr", config.SceneAddr, "scene gRPC listen address")
	flag.Parse()

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s failed: %v", *addr, err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterSceneServiceServer(grpcServer, sceneserver.New())

	log.Printf("scene gRPC listening on %s", *addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("scene server stopped: %v", err)
	}
}
