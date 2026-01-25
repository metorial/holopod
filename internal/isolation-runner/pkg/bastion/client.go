package bastion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
)

type Client struct {
	conn        *grpc.ClientConn
	client      pb.BastionServiceClient
	containerID string
}

type NetworkResult struct {
	NetworkName string
	NetworkID   string
	Subnet      string
	Reused      bool
}

func Connect(address, containerID string) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to bastion at %s: %w", address, err)
	}

	client := pb.NewBastionServiceClient(conn)

	return &Client{
		conn:        conn,
		client:      client,
		containerID: containerID,
	}, nil
}

func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) AcquireNetwork(subnet *string, leaseDurationSecs *uint32) (*NetworkResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	minIPs := uint32(254)
	driver := "bridge"

	hasher := sha256.New()
	if subnet != nil {
		hasher.Write([]byte(*subnet))
	}
	hasher.Write([]byte{byte(minIPs), byte(minIPs >> 8), byte(minIPs >> 16), byte(minIPs >> 24)})
	hasher.Write([]byte(driver))
	configHash := hex.EncodeToString(hasher.Sum(nil))

	resp, err := c.client.AcquireNetwork(ctx, &pb.AcquireNetworkRequest{
		ContainerId: c.containerID,
		NetworkConfig: &pb.NetworkConfig{
			SubnetRange: subnet,
			MinIps:      &minIPs,
			Driver:      &driver,
			ConfigHash:  configHash,
		},
		LeaseDurationSecs: leaseDurationSecs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to acquire network: %w", err)
	}

	if !resp.Success {
		errMsg := "unknown error"
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return nil, fmt.Errorf("bastion error: %s", errMsg)
	}

	return &NetworkResult{
		NetworkName: *resp.NetworkName,
		NetworkID:   *resp.NetworkId,
		Subnet:      *resp.Subnet,
		Reused:      resp.Reused,
	}, nil
}

func (c *Client) ReleaseNetwork(networkName string, forceCleanup bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.client.ReleaseNetwork(ctx, &pb.ReleaseNetworkRequest{
		ContainerId:  c.containerID,
		NetworkName:  networkName,
		ForceCleanup: &forceCleanup,
	})
	if err != nil {
		return fmt.Errorf("failed to release network: %w", err)
	}

	if !resp.Success {
		errMsg := "unknown error"
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return fmt.Errorf("bastion error: %s", errMsg)
	}

	return nil
}

func (c *Client) SetupChain(chainName, containerIP string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.client.SetupChain(ctx, &pb.SetupChainRequest{
		ChainName:   chainName,
		ContainerIp: containerIP,
		ContainerId: c.containerID,
	})
	if err != nil {
		return fmt.Errorf("failed to setup chain: %w", err)
	}

	if !resp.Success {
		errMsg := "unknown error"
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return fmt.Errorf("bastion error: %s", errMsg)
	}

	return nil
}

func (c *Client) CleanupChain(chainName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.client.CleanupChain(ctx, &pb.CleanupChainRequest{
		ChainName:   chainName,
		ContainerId: c.containerID,
	})
	if err != nil {
		return fmt.Errorf("failed to cleanup chain: %w", err)
	}

	if !resp.Success {
		errMsg := "unknown error"
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return fmt.Errorf("bastion error: %s", errMsg)
	}

	return nil
}

func (c *Client) ApplyNetworkPolicy(chainName string, policy *pb.NetworkPolicy) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.client.ApplyRules(ctx, &pb.ApplyRulesRequest{
		ChainName:   chainName,
		Policy:      policy,
		ContainerId: c.containerID,
	})
	if err != nil {
		return fmt.Errorf("failed to apply network policy: %w", err)
	}

	if !resp.Success {
		errMsg := "unknown error"
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return fmt.Errorf("bastion error: %s", errMsg)
	}

	return nil
}
