package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/metorial/fleet/holopod/internal/bastion/pkg/iptables"
	"github.com/metorial/fleet/holopod/internal/bastion/pkg/networkpool"
	"github.com/metorial/fleet/holopod/internal/bastion/pkg/validation"
	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
)

type Server struct {
	pb.UnimplementedBastionServiceServer
	version     string
	networkPool *networkpool.Pool
	logger      *slog.Logger
	chainIPs    map[string]string
	chainMu     sync.RWMutex
}

func New(version string, networkPool *networkpool.Pool, logger *slog.Logger) *Server {
	return &Server{
		version:     version,
		networkPool: networkPool,
		logger:      logger,
		chainIPs:    make(map[string]string),
	}
}

func (s *Server) SetupChain(ctx context.Context, req *pb.SetupChainRequest) (*pb.SetupChainResponse, error) {
	if err := validation.ValidateChainName(req.ChainName); err != nil {
		s.auditLog("setup_chain", req.ChainName, req.ContainerId, false)
		return &pb.SetupChainResponse{
			Success: false,
			Error:   strPtr(err.Error()),
		}, nil
	}

	containerIP, err := validation.ValidateContainerIP(req.ContainerIp)
	if err != nil {
		s.auditLog("setup_chain", req.ChainName, req.ContainerId, false)
		return &pb.SetupChainResponse{
			Success: false,
			Error:   strPtr(err.Error()),
		}, nil
	}

	if err := iptables.SetupChain(ctx, req.ChainName, containerIP); err != nil {
		s.auditLog("setup_chain", req.ChainName, req.ContainerId, false)
		return &pb.SetupChainResponse{
			Success: false,
			Error:   strPtr(err.Error()),
		}, nil
	}

	s.chainMu.Lock()
	s.chainIPs[req.ChainName] = req.ContainerIp
	s.chainMu.Unlock()

	s.auditLog("setup_chain", req.ChainName, req.ContainerId, true)
	return &pb.SetupChainResponse{
		Success: true,
	}, nil
}

func (s *Server) ApplyRules(ctx context.Context, req *pb.ApplyRulesRequest) (*pb.ApplyRulesResponse, error) {
	if err := validation.ValidateChainName(req.ChainName); err != nil {
		s.auditLog("apply_rules", req.ChainName, req.ContainerId, false)
		return &pb.ApplyRulesResponse{
			Success:      false,
			Error:        strPtr(err.Error()),
			RulesApplied: 0,
		}, nil
	}

	if req.Policy == nil {
		s.auditLog("apply_rules", req.ChainName, req.ContainerId, false)
		return nil, status.Error(codes.InvalidArgument, "network policy is required")
	}

	count, err := iptables.ApplyRules(ctx, req.ChainName, req.Policy)
	if err != nil {
		s.auditLog("apply_rules", req.ChainName, req.ContainerId, false)
		return &pb.ApplyRulesResponse{
			Success:      false,
			Error:        strPtr(err.Error()),
			RulesApplied: 0,
		}, nil
	}

	s.auditLog("apply_rules", req.ChainName, req.ContainerId, true)
	return &pb.ApplyRulesResponse{
		Success:      true,
		RulesApplied: int32(count),
	}, nil
}

func (s *Server) CleanupChain(ctx context.Context, req *pb.CleanupChainRequest) (*pb.CleanupChainResponse, error) {
	if err := validation.ValidateChainName(req.ChainName); err != nil {
		s.auditLog("cleanup_chain", req.ChainName, req.ContainerId, false)
		return &pb.CleanupChainResponse{
			Success: false,
			Error:   strPtr(err.Error()),
		}, nil
	}

	s.chainMu.RLock()
	containerIP := s.chainIPs[req.ChainName]
	s.chainMu.RUnlock()

	if err := iptables.CleanupChain(ctx, req.ChainName, containerIP); err != nil {
		s.auditLog("cleanup_chain", req.ChainName, req.ContainerId, false)
		return &pb.CleanupChainResponse{
			Success: false,
			Error:   strPtr(err.Error()),
		}, nil
	}

	s.chainMu.Lock()
	delete(s.chainIPs, req.ChainName)
	s.chainMu.Unlock()

	s.auditLog("cleanup_chain", req.ChainName, req.ContainerId, true)
	return &pb.CleanupChainResponse{
		Success: true,
	}, nil
}

func (s *Server) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	iptablesAvailable := iptables.CheckIPTables(ctx) == nil

	return &pb.HealthResponse{
		Healthy:           iptablesAvailable,
		Version:           s.version,
		IptablesAvailable: iptablesAvailable,
	}, nil
}

func (s *Server) AcquireNetwork(ctx context.Context, req *pb.AcquireNetworkRequest) (*pb.AcquireNetworkResponse, error) {
	if req.NetworkConfig == nil {
		return nil, status.Error(codes.InvalidArgument, "network config is required")
	}

	if err := validation.ValidateContainerID(req.ContainerId); err != nil {
		return &pb.AcquireNetworkResponse{
			Success: false,
			Error:   strPtr(err.Error()),
		}, nil
	}

	minIPs := uint32(254)
	if req.NetworkConfig.MinIps != nil {
		minIPs = *req.NetworkConfig.MinIps
	}

	driver := "bridge"
	if req.NetworkConfig.Driver != nil {
		driver = *req.NetworkConfig.Driver
	}

	if err := validation.ValidateConfigHash(req.NetworkConfig.ConfigHash, req.NetworkConfig.SubnetRange, minIPs, driver); err != nil {
		return &pb.AcquireNetworkResponse{
			Success: false,
			Error:   strPtr(err.Error()),
		}, nil
	}

	var leaseDuration *time.Duration
	if req.LeaseDurationSecs != nil {
		d := time.Duration(*req.LeaseDurationSecs) * time.Second
		leaseDuration = &d
	}

	result, err := s.networkPool.Acquire(ctx, req.ContainerId, req.NetworkConfig.ConfigHash, req.NetworkConfig.SubnetRange, leaseDuration)
	if err != nil {
		return &pb.AcquireNetworkResponse{
			Success: false,
			Error:   strPtr(err.Error()),
		}, nil
	}

	return &pb.AcquireNetworkResponse{
		Success:     true,
		NetworkName: &result.NetworkName,
		NetworkId:   &result.NetworkID,
		Subnet:      &result.Subnet,
		Reused:      result.Reused,
	}, nil
}

func (s *Server) ReleaseNetwork(ctx context.Context, req *pb.ReleaseNetworkRequest) (*pb.ReleaseNetworkResponse, error) {
	if err := validation.ValidateContainerID(req.ContainerId); err != nil {
		return &pb.ReleaseNetworkResponse{
			Success:   false,
			Error:     strPtr(err.Error()),
			CleanedUp: false,
		}, nil
	}

	if err := validation.ValidateNetworkName(req.NetworkName); err != nil {
		return &pb.ReleaseNetworkResponse{
			Success:   false,
			Error:     strPtr(err.Error()),
			CleanedUp: false,
		}, nil
	}

	forceCleanup := false
	if req.ForceCleanup != nil {
		forceCleanup = *req.ForceCleanup
	}

	result, err := s.networkPool.Release(ctx, req.ContainerId, req.NetworkName, forceCleanup)
	if err != nil {
		return &pb.ReleaseNetworkResponse{
			Success:   false,
			Error:     strPtr(err.Error()),
			CleanedUp: false,
		}, nil
	}

	return &pb.ReleaseNetworkResponse{
		Success:   true,
		CleanedUp: result.CleanedUp,
	}, nil
}

func (s *Server) GetNetworkStats(ctx context.Context, req *pb.NetworkStatsRequest) (*pb.NetworkStatsResponse, error) {
	stats := s.networkPool.Stats()

	return &pb.NetworkStatsResponse{
		TotalNetworks:     stats.TotalNetworks,
		ActiveNetworks:    stats.ActiveNetworks,
		PooledNetworks:    stats.PooledNetworks,
		PendingCleanup:    stats.PendingCleanup,
		Utilization:       stats.Utilization,
		Healthy:           stats.Healthy,
		SubnetUtilization: stats.SubnetUtilization,
		MaxSubnets:        stats.MaxSubnets,
	}, nil
}

func (s *Server) auditLog(operation, chainName, containerID string, success bool) {
	if success {
		s.logger.Info("privileged operation succeeded",
			"operation", operation,
			"chain_name", chainName,
			"container_id", containerID,
		)
	} else {
		s.logger.Warn("privileged operation failed",
			"operation", operation,
			"chain_name", chainName,
			"container_id", containerID,
		)
	}
}

func strPtr(s string) *string {
	return &s
}
