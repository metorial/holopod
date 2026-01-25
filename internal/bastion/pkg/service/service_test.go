package service

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/metorial/fleet/holopod/internal/bastion/pkg/networkpool"
	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
)

func TestHealth(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	server := New("1.0.0-test", nil, logger)

	ctx := context.Background()
	resp, err := server.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}

	if resp.Version != "1.0.0-test" {
		t.Errorf("Version = %s, want 1.0.0-test", resp.Version)
	}
}

func TestSetupChainValidation(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	server := New("1.0.0-test", nil, logger)

	ctx := context.Background()

	tests := []struct {
		name      string
		req       *pb.SetupChainRequest
		wantError bool
	}{
		{
			name: "invalid chain name",
			req: &pb.SetupChainRequest{
				ChainName:   "invalid-name",
				ContainerIp: "172.17.0.2",
				ContainerId: "abc123def456",
			},
			wantError: true,
		},
		{
			name: "invalid IP",
			req: &pb.SetupChainRequest{
				ChainName:   "ISO-0123456789abcdef",
				ContainerIp: "8.8.8.8",
				ContainerId: "abc123def456",
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.SetupChain(ctx, tt.req)
			if err != nil {
				t.Fatalf("SetupChain() error = %v", err)
			}
			if resp.Success && tt.wantError {
				t.Error("SetupChain() succeeded but expected failure")
			}
			if !resp.Success && !tt.wantError {
				t.Errorf("SetupChain() failed: %v", resp.Error)
			}
		})
	}
}

func TestApplyRulesValidation(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	server := New("1.0.0-test", nil, logger)

	ctx := context.Background()

	t.Run("missing policy", func(t *testing.T) {
		_, err := server.ApplyRules(ctx, &pb.ApplyRulesRequest{
			ChainName:   "ISO-0123456789abcdef",
			ContainerId: "abc123def456",
		})
		if err == nil {
			t.Error("ApplyRules() with nil policy should error")
		}
	})

	t.Run("invalid chain name", func(t *testing.T) {
		resp, err := server.ApplyRules(ctx, &pb.ApplyRulesRequest{
			ChainName:   "invalid",
			ContainerId: "abc123def456",
			Policy:      &pb.NetworkPolicy{Policy: "allow"},
		})
		if err != nil {
			t.Fatalf("ApplyRules() error = %v", err)
		}
		if resp.Success {
			t.Error("ApplyRules() with invalid chain name should fail")
		}
	})
}

func TestAcquireNetworkValidation(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := networkpool.New(ctx, stateFile)
	if err != nil {
		t.Fatalf("failed to create network pool: %v", err)
	}
	defer pool.Stop()

	server := New("1.0.0-test", pool, logger)

	t.Run("missing network config", func(t *testing.T) {
		_, err := server.AcquireNetwork(ctx, &pb.AcquireNetworkRequest{
			ContainerId: "abc123def456",
		})
		if err == nil {
			t.Error("AcquireNetwork() with nil config should error")
		}
	})

	t.Run("invalid container ID", func(t *testing.T) {
		resp, err := server.AcquireNetwork(ctx, &pb.AcquireNetworkRequest{
			ContainerId: "invalid",
			NetworkConfig: &pb.NetworkConfig{
				ConfigHash: "test-hash",
			},
		})
		if err != nil {
			t.Fatalf("AcquireNetwork() error = %v", err)
		}
		if resp.Success {
			t.Error("AcquireNetwork() with invalid container ID should fail")
		}
	})
}

func TestReleaseNetworkValidation(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := networkpool.New(ctx, stateFile)
	if err != nil {
		t.Fatalf("failed to create network pool: %v", err)
	}
	defer pool.Stop()

	server := New("1.0.0-test", pool, logger)

	t.Run("invalid container ID", func(t *testing.T) {
		resp, err := server.ReleaseNetwork(ctx, &pb.ReleaseNetworkRequest{
			ContainerId: "invalid",
			NetworkName: "iso-net-test123",
		})
		if err != nil {
			t.Fatalf("ReleaseNetwork() error = %v", err)
		}
		if resp.Success {
			t.Error("ReleaseNetwork() with invalid container ID should fail")
		}
	})

	t.Run("invalid network name", func(t *testing.T) {
		resp, err := server.ReleaseNetwork(ctx, &pb.ReleaseNetworkRequest{
			ContainerId: "abc123def456",
			NetworkName: "invalid-name",
		})
		if err != nil {
			t.Fatalf("ReleaseNetwork() error = %v", err)
		}
		if resp.Success {
			t.Error("ReleaseNetwork() with invalid network name should fail")
		}
	})
}

func TestGetNetworkStats(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := networkpool.New(ctx, stateFile)
	if err != nil {
		t.Fatalf("failed to create network pool: %v", err)
	}
	defer pool.Stop()

	server := New("1.0.0-test", pool, logger)

	_, err = server.GetNetworkStats(ctx, &pb.NetworkStatsRequest{})
	if err != nil {
		t.Fatalf("GetNetworkStats() error = %v", err)
	}
}

func dockerAvailable() bool {
	tmpDir := os.TempDir()
	stateFile := filepath.Join(tmpDir, "docker_test_check.json")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pool, err := networkpool.New(ctx, stateFile)
	if err != nil {
		return false
	}
	pool.Stop()
	os.Remove(stateFile)
	return true
}
