package container

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/config"
)

func TestNewManagerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := config.DefaultConfig()
	manager, err := NewManager("test-container", "bridge", cfg)
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	if manager == nil {
		t.Fatal("manager should not be nil")
	}

	if manager.Docker() == nil {
		t.Fatal("docker client should not be nil")
	}

	if manager.NetworkName() != "bridge" {
		t.Errorf("expected network name 'bridge', got %s", manager.NetworkName())
	}
}

func TestCheckGVisorIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := config.DefaultConfig()
	manager, err := NewManager("test-container", "bridge", cfg)
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = manager.CheckGVisor(ctx)
	if err != nil {
		t.Logf("gVisor not available (expected in many environments): %v", err)
	}
}

func TestPullImageIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := config.DefaultConfig()
	manager, err := NewManager("test-container", "bridge", cfg)
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	err = manager.PullImage(ctx, "alpine:latest", nil)
	if err != nil {
		t.Logf("Failed to pull image (might be network issue): %v", err)
	}
}

func TestDockerConnectivity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ping, err := docker.Ping(ctx)
	if err != nil {
		t.Fatalf("Docker ping failed: %v", err)
	}

	if ping.APIVersion == "" {
		t.Error("expected API version to be set")
	}

	t.Logf("Docker API version: %s", ping.APIVersion)
}
