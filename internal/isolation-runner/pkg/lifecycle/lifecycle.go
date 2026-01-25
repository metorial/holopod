package lifecycle

import (
	"context"
	"fmt"

	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/bastion"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/config"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/container"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/jsonmsg"
)

func SetupContainer(ctx context.Context, input *config.ContainerInput, cfg *config.Config) (*container.Manager, error) {
	containerName := input.GetContainerName()
	networkName := input.GetBridgeName()

	manager, err := container.NewManager(containerName, networkName, cfg)
	if err != nil {
		return nil, err
	}

	if err := manager.CheckGVisor(ctx); err != nil {
		return nil, err
	}

	bastionAddress := config.GetBastionAddress()
	// jsonmsg.Info(fmt.Sprintf("Using bastion address: %s", bastionAddress))
	bastionClient, err := bastion.Connect(bastionAddress, containerName)
	if err != nil {
		jsonmsg.Warning(fmt.Sprintf("Could not connect to bastion at %s: %v. Proceeding without bastion.", bastionAddress, err))
		return nil, fmt.Errorf("bastion connection failed: %w", err)
	}
	defer bastionClient.Close()

	if err := manager.SetupNetworkViaBastion(ctx, input.Subnet, bastionClient); err != nil {
		return nil, err
	}

	// Validate image spec
	if err := config.ValidateImageSpec(input.ImageSpec); err != nil {
		return nil, fmt.Errorf("invalid image spec: %w", err)
	}

	imageRef := input.GetFullImageReference()
	var auth *config.ImageAuth
	if input.ImageSpec != nil {
		auth = input.ImageSpec.Auth
	}

	// SECURITY: Log display name only
	// jsonmsg.Info(fmt.Sprintf("Image: %s", input.GetImageDisplayName()))

	cmd := input.GetContainerCommand()
	args := input.GetContainerArgs()
	if err := manager.CreateContainer(ctx, imageRef, cmd, args, auth); err != nil {
		_ = manager.CleanupNetwork(ctx, bastionClient)

		// SECURITY: Clear auth on error
		if auth != nil {
			auth.Username = ""
			auth.Password = ""
		}

		return nil, err
	}

	// SECURITY: Clear auth after success
	if auth != nil {
		auth.Username = ""
		auth.Password = ""
	}

	return manager, nil
}

func SetupNetworkIsolation(ctx context.Context, containerID string, containerIP string, cfg *config.Config) (string, error) {
	// CRITICAL SECURITY: Validate and enforce network security rules
	// These rules CANNOT be bypassed and include mandatory blocks for:
	// - Localhost (127.0.0.0/8, ::1/128)
	// - Cloud metadata services (169.254.169.254/32)
	// - Private IPs (unless explicitly whitelisted)
	if err := config.ValidateNetworkConfig(&cfg.Network); err != nil {
		return "", fmt.Errorf("network security validation failed: %w", err)
	}

	// jsonmsg.Info("Network security rules validated and enforced (localhost, metadata, and private IPs blocked)")
	jsonmsg.Info("Network security rules validated and enforced")

	bastionAddress := config.GetBastionAddress()

	// jsonmsg.Info(fmt.Sprintf("Connecting to Network Bastion at %s for iptables operations", bastionAddress))

	bastionClient, err := bastion.Connect(bastionAddress, containerID)
	if err != nil {
		return "", fmt.Errorf("failed to connect to Network Bastion: %w. Ensure the bastion service is running", err)
	}
	defer bastionClient.Close()

	// jsonmsg.Info("Connected to Network Bastion - all iptables operations will be validated")

	chainName := GenerateChainName(containerID)

	if err := bastionClient.SetupChain(chainName, containerIP); err != nil {
		return "", err
	}

	policy := buildNetworkPolicy(cfg)
	if err := bastionClient.ApplyNetworkPolicy(chainName, policy); err != nil {
		return "", err
	}

	// jsonmsg.Info(fmt.Sprintf("Network isolation configured: chain %s created via bastion", chainName))
	jsonmsg.NetworkIsolationReady(containerID, chainName, cfg.Network.DefaultPolicy)

	return chainName, nil
}

func CleanupNetworkIsolation(ctx context.Context, chainName string) {
	jsonmsg.Info("Cleaning up network isolation")

	bastionAddress := config.GetBastionAddress()

	bastionClient, err := bastion.Connect(bastionAddress, "cleanup")
	if err != nil {
		jsonmsg.Warning(fmt.Sprintf("Could not connect to bastion for cleanup: %v", err))
		return
	}
	defer bastionClient.Close()

	if err := bastionClient.CleanupChain(chainName); err != nil {
		jsonmsg.Warning(fmt.Sprintf("Failed to cleanup network rules via bastion: %v", err))
	} else {
		jsonmsg.Info("Network isolation cleaned up successfully")
	}
}

func GenerateChainName(containerID string) string {
	hexPart := ""
	for _, ch := range containerID {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			hexPart += string(ch)
			if len(hexPart) == 16 {
				break
			}
		}
	}
	return fmt.Sprintf("ISO-%s", hexPart)
}

func buildNetworkPolicy(cfg *config.Config) *pb.NetworkPolicy {
	policy := &pb.NetworkPolicy{
		Policy:        cfg.Network.DefaultPolicy,
		BlockMetadata: cfg.Network.BlockMetadata,
		AllowDns:      cfg.Network.AllowDNS,
		DnsServers:    cfg.Network.DNSServers,
		Whitelist:     make([]*pb.NetworkRule, 0),
		Blacklist:     make([]*pb.NetworkRule, 0),
	}

	for _, entry := range cfg.Network.Whitelist {
		ports := make([]uint32, 0, len(entry.Ports))
		for _, p := range entry.Ports {
			var port uint32
			fmt.Sscanf(p, "%d", &port)
			ports = append(ports, port)
		}

		policy.Whitelist = append(policy.Whitelist, &pb.NetworkRule{
			Cidr:        entry.CIDR,
			Description: &entry.Description,
			Ports:       ports,
		})
	}

	for _, entry := range cfg.Network.Blacklist {
		policy.Blacklist = append(policy.Blacklist, &pb.NetworkRule{
			Cidr:        entry.CIDR,
			Description: &entry.Description,
			Ports:       []uint32{},
		})
	}

	return policy
}
