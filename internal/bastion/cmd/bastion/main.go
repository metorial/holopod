package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/metorial/fleet/holopod/internal/bastion/pkg/iptables"
	"github.com/metorial/fleet/holopod/internal/bastion/pkg/networkpool"
	"github.com/metorial/fleet/holopod/internal/bastion/pkg/service"
	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
)

const version = "1.0.0"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !skipRootCheck() {
		if os.Getuid() != 0 {
			logger.Error("bastion service must run as root for iptables operations")
			logger.Error("run with: sudo ./bastion")
			logger.Error("or set BASTION_SKIP_ROOT_CHECK=true for testing network pool only")
			os.Exit(1)
		}
	} else {
		logger.Info("root check skipped for testing", "BASTION_SKIP_ROOT_CHECK", "true")
		logger.Info("iptables operations will fail without root privileges")
	}

	logger.Info("bastion service starting", "version", version)
	logger.Info("service runs with elevated privileges to manage iptables")

	if err := iptables.CheckIPTables(ctx); err != nil {
		logger.Error("iptables check failed", "error", err)
		logger.Error("ensure iptables is installed and accessible")
		os.Exit(1)
	}

	logger.Info("initializing network pool")
	stateFile := os.Getenv("BASTION_STATE_FILE")
	if stateFile != "" {
		logger.Info("using custom state file", "path", stateFile)
	}

	pool, err := networkpool.New(ctx, stateFile)
	if err != nil {
		logger.Error("failed to initialize network pool", "error", err)
		os.Exit(1)
	}

	pool.StartCleanup(ctx)
	logger.Info("network pool initialized and cleanup task started")

	listenAddr := os.Getenv("LISTEN_ADDRESS")
	if listenAddr == "" {
		listenAddr = "0.0.0.0:50054"
	}

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		logger.Error("failed to listen", "address", listenAddr, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	bastionService := service.New(version, pool, logger)
	pb.RegisterBastionServiceServer(grpcServer, bastionService)

	logger.Info("starting gRPC bastion service", "address", listenAddr)
	logger.Info("security: all operations are validated and audit logged")
	logger.Info("network pool: automatic cleanup every 5 minutes, TTL 1 hour")

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			logger.Error("gRPC server failed", "error", err)
			os.Exit(1)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("shutting down gracefully")
	grpcServer.GracefulStop()
	pool.Stop()
	logger.Info("shutdown complete")
}

func skipRootCheck() bool {
	val := os.Getenv("BASTION_SKIP_ROOT_CHECK")
	return val == "true" || val == "1"
}
