package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/config"
)

func TestGenerateChainNameIntegration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		containerID string
	}{
		{"full length ID", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"},
		{"short ID", "abc123"},
		{"with special chars", "container-id-with-dashes"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chainName := GenerateChainName(tt.containerID)
			if len(chainName) > 20 {
				t.Errorf("chain name too long: %d chars", len(chainName))
			}
			if len(chainName) == 0 {
				t.Error("chain name is empty")
			}
		})
	}
}

func TestResourceTrackerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := docker.Ping(ctx); err != nil {
		t.Skipf("Docker daemon not responding: %v", err)
	}

	tracker := NewResourceTracker(docker)
	if tracker == nil {
		t.Fatal("tracker should not be nil")
	}

	tracker.TrackContainer("test-container-id", "test-container")
	tracker.TrackNetwork("test-network", false)
	tracker.TrackChain("ISO-test")

	tracker.UntrackChain()
	tracker.UntrackContainer()
	tracker.UntrackNetwork()

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()
	tracker.CleanupAll(cleanupCtx)
}

func TestDefaultConfigIntegration(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()

	if cfg.Network.DefaultPolicy != "deny" {
		t.Errorf("expected default policy deny, got %s", cfg.Network.DefaultPolicy)
	}

	if !cfg.Network.BlockMetadata {
		t.Error("expected metadata blocking to be enabled")
	}

	if cfg.Container.Runtime != "runsc" {
		t.Errorf("expected gVisor runtime, got %s", cfg.Container.Runtime)
	}

	if !cfg.Execution.AutoCleanup {
		t.Error("expected auto cleanup to be enabled")
	}
}
