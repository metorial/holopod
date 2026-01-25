package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/metorial/fleet/holopod/services/container-manager/pkg/manager"
	"github.com/metorial/fleet/holopod/services/container-manager/pkg/service"
	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
	"google.golang.org/grpc"
)

const version = "1.0.0"

func main() {
	listenAddr := os.Getenv("LISTEN_ADDRESS")
	if listenAddr == "" {
		listenAddr = "0.0.0.0:50051"
	}

	log.Printf("Container Manager v%s starting...", version)
	log.Printf("Listen address: %s", listenAddr)

	mgr, err := manager.New()
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}
	defer mgr.Stop()

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	svc := service.New(mgr)
	pb.RegisterContainerManagerServer(grpcServer, svc)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal, stopping...")
		grpcServer.GracefulStop()
	}()

	log.Printf("Container Manager gRPC server listening on %s", listenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}

	log.Println("Container Manager stopped")
}
