package manager

import (
	"context"
	"os"
	"testing"
	"time"

	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
)

func setupTestManager(t *testing.T) *Manager {
	// Set a fake isolation runner path to avoid search
	os.Setenv("ISOLATION_RUNNER_PATH", "/tmp/fake-runner")
	t.Cleanup(func() {
		os.Unsetenv("ISOLATION_RUNNER_PATH")
	})

	m, err := New()
	if err != nil {
		t.Skipf("Skipping test: %v", err)
		return nil
	}
	t.Cleanup(func() {
		m.Stop()
	})

	return m
}

func TestNew(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	if m.containers == nil {
		t.Error("containers map should be initialized")
	}
	if m.maxContainers != DefaultMaxContainers {
		t.Errorf("Expected max containers %d, got %d", DefaultMaxContainers, m.maxContainers)
	}
}

func TestManagerStats(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	total, running := m.GetStats()
	if total != 0 {
		t.Errorf("Expected 0 total containers, got %d", total)
	}
	if running != 0 {
		t.Errorf("Expected 0 running containers, got %d", running)
	}
}

func TestListContainersEmpty(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	containers := m.ListContainers("all")
	if len(containers) != 0 {
		t.Errorf("Expected 0 containers, got %d", len(containers))
	}
}

func TestGetContainerNotFound(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	_, err := m.GetContainer("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent container")
	}
}

func TestTerminateContainerNotFound(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	err := m.TerminateContainer("nonexistent", false, 5)
	if err == nil {
		t.Error("Expected error for nonexistent container")
	}
}

func TestWaitContainerNotFound(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	_, err := m.WaitContainer("nonexistent", 5)
	if err == nil {
		t.Error("Expected error for nonexistent container")
	}
}

func TestGetContainerStatusNotFound(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	_, err := m.GetContainerStatus("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent container")
	}
}

func TestCleanupExitedContainersNow(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	// No containers to cleanup
	count := m.CleanupExitedContainersNow()
	if count != 0 {
		t.Errorf("Expected 0 cleaned up, got %d", count)
	}
}

func TestMaxContainersConfiguration(t *testing.T) {
	os.Setenv("MAX_CONTAINERS_PER_MANAGER", "100")
	os.Setenv("ISOLATION_RUNNER_PATH", "/tmp/fake-runner")
	defer os.Unsetenv("MAX_CONTAINERS_PER_MANAGER")
	defer os.Unsetenv("ISOLATION_RUNNER_PATH")

	m, err := New()
	if err != nil {
		t.Skipf("Skipping test: %v", err)
		return
	}
	defer m.Stop()

	if m.maxContainers != 100 {
		t.Errorf("Expected max containers 100, got %d", m.maxContainers)
	}
}

func TestFindIsolationRunner(t *testing.T) {
	// Test env var takes precedence
	os.Setenv("ISOLATION_RUNNER_PATH", "/tmp/test-runner")
	defer os.Unsetenv("ISOLATION_RUNNER_PATH")

	// Create the file
	f, err := os.Create("/tmp/test-runner")
	if err != nil {
		t.Skipf("Cannot create test file: %v", err)
		return
	}
	f.Close()
	defer os.Remove("/tmp/test-runner")

	path, err := findIsolationRunner()
	if err != nil {
		t.Errorf("Failed to find runner: %v", err)
	}
	if path != "/tmp/test-runner" {
		t.Errorf("Expected /tmp/test-runner, got %s", path)
	}
}

func TestFindIsolationRunnerNotFound(t *testing.T) {
	// Make sure env var is not set
	os.Unsetenv("ISOLATION_RUNNER_PATH")

	// Temporarily move PATH
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", oldPath)

	_, err := findIsolationRunner()
	if err == nil {
		t.Error("Expected error when runner not found")
	}
}

func TestManagerStop(t *testing.T) {
	os.Setenv("ISOLATION_RUNNER_PATH", "/tmp/fake-runner")
	defer os.Unsetenv("ISOLATION_RUNNER_PATH")

	m, err := New()
	if err != nil {
		t.Skipf("Skipping test: %v", err)
		return
	}

	// Stop should not hang
	done := make(chan bool)
	go func() {
		m.Stop()
		done <- true
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Stop() timed out")
	}
}

func TestListContainersFilters(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	// Test different filter values don't crash
	filters := []string{"all", "running", "exited", "", "invalid"}
	for _, filter := range filters {
		containers := m.ListContainers(filter)
		if containers == nil {
			t.Errorf("ListContainers returned nil for filter '%s'", filter)
		}
	}
}

func TestCreateContainerFailsWithoutRunner(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "ubuntu:latest",
		},
		Command: []string{"echo", "hello"},
	}

	// This will fail because the runner doesn't exist
	_, err := m.CreateContainer(context.Background(), "", config)
	if err == nil {
		t.Error("Expected error when runner doesn't exist")
	}

	// Container should not be in the map since creation failed
	total, _ := m.GetStats()
	if total != 0 {
		t.Errorf("Expected 0 containers after failed creation, got %d", total)
	}
}

func TestCreateContainerGeneratesID(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	config := &pb.ContainerConfig{ImageSpec: &pb.ImageSpec{Image: "test"}}

	// Pass empty ID - should generate UUID
	id, _ := m.CreateContainer(context.Background(), "", config)

	// Will fail to start, but ID should be generated
	if id == "" {
		t.Error("Expected generated container ID")
	}

	// Try with another empty ID - should generate different ID
	id2, _ := m.CreateContainer(context.Background(), "", config)
	if id2 == "" {
		t.Error("Expected generated container ID")
	}
	if id == id2 {
		t.Error("Expected different generated IDs")
	}
}

func TestBufferTracking(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

}

func TestDefaultConstants(t *testing.T) {
	if CleanupIntervalSecs != 300 {
		t.Errorf("Expected CleanupIntervalSecs 300, got %d", CleanupIntervalSecs)
	}
	if DefaultMaxContainers != 1000 {
		t.Errorf("Expected DefaultMaxContainers 1000, got %d", DefaultMaxContainers)
	}
}

func TestCleanupTaskStartsAutomatically(t *testing.T) {
	m := setupTestManager(t)
	if m == nil {
		return
	}

	// Cleanup task should be running
	// We can't easily test its behavior without creating real containers,
	// but we can verify Stop works
	done := make(chan bool)
	go func() {
		m.Stop()
		done <- true
	}()

	select {
	case <-done:
		// Success - cleanup task stopped properly
	case <-time.After(2 * time.Second):
		t.Error("Cleanup task didn't stop properly")
	}
}
