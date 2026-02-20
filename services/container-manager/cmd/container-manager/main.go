package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/metorial/fleet/holopod/services/container-manager/pkg/manager"
	"github.com/metorial/fleet/holopod/services/container-manager/pkg/publicapi"
	"github.com/metorial/fleet/holopod/services/container-manager/pkg/service"
	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

var version = "dev"

func main() {
	listenAddr := os.Getenv("LISTEN_ADDRESS")
	if listenAddr == "" {
		listenAddr = "0.0.0.0:50051"
	}
	httpListenAddr := os.Getenv("HTTP_LISTEN_ADDRESS")
	if httpListenAddr == "" {
		httpListenAddr = "0.0.0.0:3000"
	}
	grpcDialAddr := os.Getenv("GRPC_DIAL_ADDRESS")
	if grpcDialAddr == "" {
		grpcDialAddr = listenAddr
		if strings.HasPrefix(grpcDialAddr, "0.0.0.0:") {
			grpcDialAddr = "127.0.0.1:" + strings.TrimPrefix(grpcDialAddr, "0.0.0.0:")
		}
	}

	log.Printf("Container Manager v%s starting...", version)
	log.Printf("gRPC listen address: %s", listenAddr)
	log.Printf("HTTP listen address: %s", httpListenAddr)

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
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	svc := service.New(mgr)
	pb.RegisterContainerManagerServer(grpcServer, svc)

	publicServer, err := publicapi.NewServer(grpcDialAddr)
	if err != nil {
		log.Fatalf("Failed to create public API server: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", publicServer.HandleHealth)
	mux.HandleFunc("/v1/run", publicServer.HandleRun)
	httpServer := &http.Server{
		Addr:    httpListenAddr,
		Handler: mux,
	}

	go func() {
		log.Printf("Container Manager public HTTP server listening on %s", httpListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to serve HTTP API: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal, stopping...")
		_ = httpServer.Close()
		grpcServer.GracefulStop()
	}()

	log.Printf("Container Manager gRPC server listening on %s", listenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}

	log.Println("Container Manager stopped")
}
