package container

import (
	"testing"
	"time"

	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
)

func TestNewContainer(t *testing.T) {
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "ubuntu:latest",
		},
		Command: []string{"echo", "hello"},
	}

	c := New("test-id", config)

	if c.ID != "test-id" {
		t.Errorf("Expected ID 'test-id', got '%s'", c.ID)
	}

	if c.Config.ImageSpec.Image != "ubuntu:latest" {
		t.Errorf("Expected image 'ubuntu:latest', got '%s'", c.Config.ImageSpec.Image)
	}

	state := c.GetState()
	if state.State != pb.ContainerState_CREATED {
		t.Errorf("Expected state CREATED, got %v", state.State)
	}

	if state.ContainerId != "test-id" {
		t.Errorf("Expected container_id 'test-id', got '%s'", state.ContainerId)
	}

	if state.Config == nil {
		t.Error("Expected config to be set in state")
	} else if state.Config.ImageSpec == nil || state.Config.ImageSpec.Image != "ubuntu:latest" {
		t.Errorf("Expected config image 'ubuntu:latest'")
	}
}

func TestGetState(t *testing.T) {
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "test",
		},
		Command: []string{"echo", "test"},
	}
	c := New("test", config)

	state := c.GetState()

	if state.State != pb.ContainerState_CREATED {
		t.Errorf("Expected CREATED state, got %v", state.State)
	}

	if state.ContainerId != "test" {
		t.Errorf("Expected container_id 'test', got '%s'", state.ContainerId)
	}

	if state.CreatedAt == "" {
		t.Error("Expected CreatedAt to be set")
	}

	if state.Config == nil {
		t.Error("Expected Config to be set")
	}
}

func TestTerminateWithoutProcess(t *testing.T) {
	config := &pb.ContainerConfig{ImageSpec: &pb.ImageSpec{Image: "test"}}
	c := New("test", config)

	// Terminate should not fail even without process
	err := c.Terminate(false, 5)
	if err != nil {
		t.Errorf("Terminate failed: %v", err)
	}

	state := c.GetState()
	if state.State != pb.ContainerState_TERMINATED {
		t.Errorf("Expected TERMINATED state, got %v", state.State)
	}
}

func TestWaitWithoutProcess(t *testing.T) {
	config := &pb.ContainerConfig{ImageSpec: &pb.ImageSpec{Image: "test"}}
	c := New("test", config)

	// Don't close the channel - let Wait timeout naturally
	// Wait should timeout since there's no process sending exit code
	done := make(chan bool)
	go func() {
		_, err := c.Wait(1)
		if err == nil {
			t.Error("Expected timeout error")
		}
		done <- true
	}()

	select {
	case <-done:
		// Success
	case <-time.After(3 * time.Second):
		t.Error("Wait hung - should have timed out")
	}
}

func TestConfigInState(t *testing.T) {
	cpuLimit := "1.0"
	memLimit := "512MB"
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "ubuntu:latest",
		},
		Command: []string{"echo", "hello"},
		Env:     map[string]string{"KEY": "value"},
		Resources: &pb.ResourceLimits{
			CpuLimit:    &cpuLimit,
			MemoryLimit: &memLimit,
		},
	}
	c := New("test", config)

	state := c.GetState()
	if state.Config == nil {
		t.Fatal("Config is nil in state")
	}
	if state.Config.ImageSpec == nil || state.Config.ImageSpec.Image != "ubuntu:latest" {
		t.Errorf("Image mismatch in state")
	}
	if len(state.Config.Command) != 2 || state.Config.Command[0] != "echo" {
		t.Errorf("Command mismatch in state")
	}
	if state.Config.Env["KEY"] != "value" {
		t.Errorf("Env mismatch in state")
	}
	if state.Config.Resources == nil {
		t.Error("Resources should be set")
	}
}

func TestTimestamps(t *testing.T) {
	config := &pb.ContainerConfig{ImageSpec: &pb.ImageSpec{Image: "test"}}
	c := New("test", config)

	state := c.GetState()
	if state.CreatedAt == "" {
		t.Error("CreatedAt should be set")
	}

	// Verify it's a valid unix timestamp string
	if len(state.CreatedAt) < 10 {
		t.Errorf("CreatedAt doesn't look like a unix timestamp: %s", state.CreatedAt)
	}
}

func TestResourceLimits(t *testing.T) {
	cpuLimit := "1.0"
	memLimit := "512MB"
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "test",
		},
		Resources: &pb.ResourceLimits{
			CpuLimit:    &cpuLimit,
			MemoryLimit: &memLimit,
		},
	}
	c := New("test", config)

	state := c.GetState()
	if state.Config.Resources == nil {
		t.Fatal("Resources should be set")
	}
	if state.Config.Resources.CpuLimit == nil || *state.Config.Resources.CpuLimit != "1.0" {
		t.Error("CPU limit mismatch")
	}
	if state.Config.Resources.MemoryLimit == nil || *state.Config.Resources.MemoryLimit != "512MB" {
		t.Error("Memory limit mismatch")
	}
}

func TestTimeout(t *testing.T) {
	timeout := uint32(300)
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "test",
		},
		TimeoutSecs: &timeout,
	}
	c := New("test", config)

	state := c.GetState()
	if state.Config.TimeoutSecs == nil || *state.Config.TimeoutSecs != 300 {
		t.Error("Timeout mismatch")
	}
}

func TestWorkdir(t *testing.T) {
	workdir := "/app"
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "test",
		},
		Workdir: &workdir,
	}
	c := New("test", config)

	state := c.GetState()
	if state.Config.Workdir == nil || *state.Config.Workdir != "/app" {
		t.Error("Workdir mismatch")
	}
}

func TestCloseIdempotent(t *testing.T) {
	config := &pb.ContainerConfig{ImageSpec: &pb.ImageSpec{Image: "test"}}
	c := New("test", config)

	// Close multiple times should not panic
	c.Close()
	c.Close()
	c.Close()
}

func TestSubscribeChannels(t *testing.T) {
	config := &pb.ContainerConfig{ImageSpec: &pb.ImageSpec{Image: "test"}}
	c := New("test", config)

	// Subscribe to channels
	stdoutCh := c.SubscribeStdout()
	stderrCh := c.SubscribeStderr()
	msgCh := c.SubscribeMessages()

	// Channels should not be nil
	if stdoutCh == nil {
		t.Error("Stdout channel is nil")
	}
	if stderrCh == nil {
		t.Error("Stderr channel is nil")
	}
	if msgCh == nil {
		t.Error("Message channel is nil")
	}

	// Close container - channels should close
	c.Close()

	// Try to receive - should get closed channel
	select {
	case _, ok := <-stdoutCh:
		if ok {
			t.Error("Expected stdout channel to be closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Timeout waiting for channel close")
	}
}

func TestWriteStdinWithoutProcess(t *testing.T) {
	config := &pb.ContainerConfig{ImageSpec: &pb.ImageSpec{Image: "test"}}
	c := New("test", config)

	// Writing stdin without a process should fail gracefully
	err := c.WriteStdin([]byte("test\n"))
	if err == nil {
		t.Error("Expected error when writing stdin without process")
	}
}

func TestCleanupFlag(t *testing.T) {
	cleanup := true
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "test",
		},
		Cleanup: &cleanup,
	}
	c := New("test", config)

	state := c.GetState()
	if state.Config.Cleanup == nil || *state.Config.Cleanup != true {
		t.Error("Cleanup flag mismatch")
	}
}

func TestNoCleanup(t *testing.T) {
	cleanup := false
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "test",
		},
		Cleanup: &cleanup,
	}
	c := New("test", config)

	state := c.GetState()
	if state.Config.Cleanup == nil || *state.Config.Cleanup != false {
		t.Error("Cleanup flag should be false")
	}
}

func TestNetworkConfig(t *testing.T) {
	defaultPolicy := "deny"
	config := &pb.ContainerConfig{
		ImageSpec: &pb.ImageSpec{
			Image: "test",
		},
		Network: &pb.NetworkConfig{
			DefaultPolicy: &defaultPolicy,
			DnsServers:    []string{"8.8.8.8"},
		},
	}
	c := New("test", config)

	state := c.GetState()
	if state.Config.Network == nil {
		t.Fatal("Network config should be set")
	}
	if state.Config.Network.DefaultPolicy == nil || *state.Config.Network.DefaultPolicy != "deny" {
		t.Error("Network default policy mismatch")
	}
	if len(state.Config.Network.DnsServers) != 1 || state.Config.Network.DnsServers[0] != "8.8.8.8" {
		t.Error("DNS servers mismatch")
	}
}
