package bastion_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/metorial/fleet/holopod/internal/bastion/pkg/networkpool"
	"github.com/metorial/fleet/holopod/internal/bastion/pkg/service"
	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
)

func TestIntegrationHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	server := service.New("1.0.0-test", nil, logger)

	ctx := context.Background()
	resp, err := server.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}

	if resp.Version != "1.0.0-test" {
		t.Errorf("Version = %s, want 1.0.0-test", resp.Version)
	}

	if !resp.IptablesAvailable {
		t.Log("iptables not available (expected in container environment)")
	}
}

func TestIntegrationNetworkPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := networkpool.New(ctx, stateFile)
	if err != nil {
		t.Fatalf("failed to create network pool: %v", err)
	}
	defer pool.Stop()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	server := service.New("1.0.0-test", pool, logger)

	_, err = server.GetNetworkStats(ctx, &pb.NetworkStatsRequest{})
	if err != nil {
		t.Fatalf("GetNetworkStats() error = %v", err)
	}
}

func dockerAvailable() bool {
	tmpDir := os.TempDir()
	stateFile := filepath.Join(tmpDir, "docker_check.json")
	defer os.Remove(stateFile)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pool, err := networkpool.New(ctx, stateFile)
	if err != nil {
		return false
	}
	pool.Stop()
	return true
}
