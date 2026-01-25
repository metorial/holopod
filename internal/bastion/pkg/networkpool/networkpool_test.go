package networkpool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Stop()

	if pool.state == nil {
		t.Error("pool state is nil")
	}
	if pool.docker == nil {
		t.Error("pool docker client is nil")
	}
}

func TestAcquireAndRelease(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Stop()

	containerID := "abc123def456"
	configHash := "test-hash-1"

	result, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	if result.NetworkName == "" {
		t.Error("acquired network name is empty")
	}
	if result.NetworkID == "" {
		t.Error("acquired network ID is empty")
	}
	if result.Subnet == "" {
		t.Error("acquired subnet is empty")
	}
	if result.Reused {
		t.Error("first acquisition should not be reused")
	}

	releaseResult, err := pool.Release(ctx, containerID, result.NetworkName, false)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if releaseResult.CleanedUp {
		t.Error("network should not be immediately cleaned up")
	}

	reuseResult, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
	if err != nil {
		t.Fatalf("second Acquire() error = %v", err)
	}
	if !reuseResult.Reused {
		t.Error("second acquisition should be reused")
	}
	if reuseResult.NetworkName != result.NetworkName {
		t.Errorf("network name mismatch: got %s, want %s", reuseResult.NetworkName, result.NetworkName)
	}

	_, err = pool.Release(ctx, containerID, result.NetworkName, true)
	if err != nil {
		t.Fatalf("forced Release() error = %v", err)
	}
}

func TestReleaseErrors(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Stop()

	_, err = pool.Release(ctx, "fake-container", "fake-network", false)
	if err == nil {
		t.Error("Release() with non-existent network should error")
	}

	containerID := "abc123def456"
	configHash := "test-hash-2"
	result, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer pool.Release(ctx, containerID, result.NetworkName, true)

	_, err = pool.Release(ctx, "wrong-container", result.NetworkName, false)
	if err == nil {
		t.Error("Release() with wrong container ID should error")
	}
}

func TestStats(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Stop()

	stats := pool.Stats()
	if stats.TotalNetworks != 0 {
		t.Errorf("initial total networks = %d, want 0", stats.TotalNetworks)
	}

	containerID := "abc123def456"
	configHash := "test-hash-3"
	result, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer pool.Release(ctx, containerID, result.NetworkName, true)

	stats = pool.Stats()
	if stats.TotalNetworks != 1 {
		t.Errorf("total networks = %d, want 1", stats.TotalNetworks)
	}
	if stats.ActiveNetworks != 1 {
		t.Errorf("active networks = %d, want 1", stats.ActiveNetworks)
	}
}

func TestPersistence(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()

	pool1, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	containerID := "abc123def456"
	configHash := "test-hash-4"
	result1, err := pool1.Acquire(ctx, containerID, configHash, nil, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	pool1.Stop()

	pool2, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}
	defer pool2.Stop()

	stats := pool2.Stats()
	if stats.TotalNetworks == 0 {
		t.Error("persisted network not found after reload")
	}

	pool2.Release(ctx, containerID, result1.NetworkName, true)
}

func TestAllocateSubnet(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Stop()

	// Test 1: Without any networks, should return first available subnet consistently
	subnet1, err := pool.allocateSubnet(ctx)
	if err != nil {
		t.Fatalf("allocateSubnet() error = %v", err)
	}
	if subnet1 == "" {
		t.Error("allocated subnet is empty")
	}

	subnet2, err := pool.allocateSubnet(ctx)
	if err != nil {
		t.Fatalf("allocateSubnet() second call error = %v", err)
	}
	if subnet1 != subnet2 {
		t.Errorf("allocateSubnet() should return same subnet when no networks exist, got %s and %s", subnet1, subnet2)
	}

	// Test 2: After creating networks via Acquire, should get different subnets
	containerIDs := []string{"test-1", "test-2", "test-3"}
	acquiredSubnets := make(map[string]bool)

	for _, containerID := range containerIDs {
		result, err := pool.Acquire(ctx, containerID, "test-config", nil, nil)
		if err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
		acquiredSubnets[result.Subnet] = true
	}

	// All acquired subnets should be unique
	if len(acquiredSubnets) != len(containerIDs) {
		t.Errorf("expected %d unique subnets, got %d", len(containerIDs), len(acquiredSubnets))
	}

	// Cleanup
	for i, containerID := range containerIDs {
		// Find the actual network name from pool state
		pool.state.mu.RLock()
		var actualNetworkName string
		for name, entry := range pool.state.Networks {
			if entry.CurrentContainer != nil && *entry.CurrentContainer == containerID {
				actualNetworkName = name
				break
			}
		}
		pool.state.mu.RUnlock()

		if actualNetworkName != "" {
			if _, err := pool.Release(ctx, containerID, actualNetworkName, true); err != nil {
				t.Logf("Release() cleanup error for container %d: %v", i, err)
			}
		}
	}
}

func TestCleanup(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Stop()

	containerID := "abc123def456"
	configHash := "test-hash-5"
	result, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	pool.Release(ctx, containerID, result.NetworkName, false)

	pool.state.mu.Lock()
	entry := pool.state.Networks[result.NetworkName]
	past := time.Now().Add(-2 * time.Hour)
	entry.CleanupAt = &past
	pool.state.mu.Unlock()

	if err := pool.runCleanup(ctx); err != nil {
		t.Fatalf("runCleanup() error = %v", err)
	}

	stats := pool.Stats()
	if stats.TotalNetworks != 0 {
		t.Errorf("network not cleaned up: total networks = %d", stats.TotalNetworks)
	}
}

func TestAtomicPersist(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")

	ctx := context.Background()
	pool, err := New(ctx, stateFile)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Stop()

	containerID := "atomic-test-123"
	configHash := "test-hash-atomic"
	result, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer pool.Release(ctx, containerID, result.NetworkName, true)

	tmpFile := stateFile + ".tmp"
	if _, err := os.Stat(tmpFile); err == nil {
		t.Errorf("temporary file %s still exists after persist", tmpFile)
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}
	if len(data) == 0 {
		t.Error("state file is empty")
	}
}

func dockerAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tmpDir := os.TempDir()
	stateFile := filepath.Join(tmpDir, "docker_check.json")
	defer os.Remove(stateFile)

	docker, err := New(ctx, stateFile)
	if err != nil {
		return false
	}
	docker.Stop()
	return true
}
