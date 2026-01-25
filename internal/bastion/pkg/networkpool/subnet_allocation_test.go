package networkpool

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSubnetConfigFromEnv(t *testing.T) {
	originalBase := os.Getenv("BASTION_SUBNET_BASE")
	originalMask := os.Getenv("BASTION_SUBNET_MASK")
	defer func() {
		os.Setenv("BASTION_SUBNET_BASE", originalBase)
		os.Setenv("BASTION_SUBNET_MASK", originalMask)
	}()

	t.Run("default configuration", func(t *testing.T) {
		os.Unsetenv("BASTION_SUBNET_BASE")
		os.Unsetenv("BASTION_SUBNET_MASK")

		config := SubnetConfigFromEnv()

		if config.BaseIP != defaultSubnetRangeBase {
			t.Errorf("BaseIP = %s, want %s", config.BaseIP, defaultSubnetRangeBase)
		}
		if config.SubnetMask != defaultSubnetMask {
			t.Errorf("SubnetMask = %d, want %d", config.SubnetMask, defaultSubnetMask)
		}
		if config.MaxSubnets != 65536 {
			t.Errorf("MaxSubnets = %d, want 65536", config.MaxSubnets)
		}
	})

	t.Run("custom base IP", func(t *testing.T) {
		os.Setenv("BASTION_SUBNET_BASE", "192.168.0.0")
		os.Unsetenv("BASTION_SUBNET_MASK")

		config := SubnetConfigFromEnv()

		if config.BaseIP != "192.168.0.0" {
			t.Errorf("BaseIP = %s, want 192.168.0.0", config.BaseIP)
		}
	})

	t.Run("custom subnet mask", func(t *testing.T) {
		os.Unsetenv("BASTION_SUBNET_BASE")
		os.Setenv("BASTION_SUBNET_MASK", "20")

		config := SubnetConfigFromEnv()

		if config.SubnetMask != 20 {
			t.Errorf("SubnetMask = %d, want 20", config.SubnetMask)
		}
		if config.MaxSubnets != 16 {
			t.Errorf("MaxSubnets = %d, want 16 (2^(24-20))", config.MaxSubnets)
		}
	})

	t.Run("invalid subnet mask ignored", func(t *testing.T) {
		os.Unsetenv("BASTION_SUBNET_BASE")
		os.Setenv("BASTION_SUBNET_MASK", "invalid")

		config := SubnetConfigFromEnv()

		if config.SubnetMask != defaultSubnetMask {
			t.Errorf("SubnetMask = %d, want %d (default)", config.SubnetMask, defaultSubnetMask)
		}
	})

	t.Run("subnet mask out of range ignored", func(t *testing.T) {
		os.Unsetenv("BASTION_SUBNET_BASE")
		os.Setenv("BASTION_SUBNET_MASK", "30")

		config := SubnetConfigFromEnv()

		if config.SubnetMask != defaultSubnetMask {
			t.Errorf("SubnetMask = %d, want %d (default for out of range)", config.SubnetMask, defaultSubnetMask)
		}
	})
}

func TestGenerateSubnet(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")
	ctx := context.Background()

	tests := []struct {
		name       string
		config     SubnetConfig
		index      int
		wantSubnet string
	}{
		{
			name: "10.20.0.0/16 range index 0",
			config: SubnetConfig{
				BaseIP:     "10.20.0.0",
				SubnetMask: 16,
				MaxSubnets: 65536,
			},
			index:      0,
			wantSubnet: "10.20.0.0/24",
		},
		{
			name: "10.20.0.0/16 range index 1",
			config: SubnetConfig{
				BaseIP:     "10.20.0.0",
				SubnetMask: 16,
				MaxSubnets: 65536,
			},
			index:      1,
			wantSubnet: "10.20.1.0/24",
		},
		{
			name: "10.20.0.0/16 range index 256",
			config: SubnetConfig{
				BaseIP:     "10.20.0.0",
				SubnetMask: 16,
				MaxSubnets: 65536,
			},
			index:      256,
			wantSubnet: "10.21.0.0/24",
		},
		{
			name: "172.20.0.0/16 range index 0",
			config: SubnetConfig{
				BaseIP:     "172.20.0.0",
				SubnetMask: 16,
				MaxSubnets: 65536,
			},
			index:      0,
			wantSubnet: "172.20.0.0/24",
		},
		{
			name: "172.20.0.0/16 range index 255",
			config: SubnetConfig{
				BaseIP:     "172.20.0.0",
				SubnetMask: 16,
				MaxSubnets: 65536,
			},
			index:      255,
			wantSubnet: "172.20.255.0/24",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewWithConfig(ctx, stateFile, tt.config, slog.Default())
			if err != nil {
				t.Fatalf("NewWithConfig() error = %v", err)
			}
			defer pool.Stop()

			baseIP := parseIP(tt.config.BaseIP)
			if baseIP == nil {
				t.Fatalf("invalid base IP: %s", tt.config.BaseIP)
			}

			gotSubnet := pool.generateSubnet(baseIP, tt.index)

			if gotSubnet != tt.wantSubnet {
				t.Errorf("generateSubnet() = %s, want %s", gotSubnet, tt.wantSubnet)
			}
		})
	}
}

func TestLargeSubnetRange(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")
	ctx := context.Background()

	config := SubnetConfig{
		BaseIP:     "10.20.0.0",
		SubnetMask: 16,
		MaxSubnets: 65536,
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	pool, err := NewWithConfig(ctx, stateFile, config, logger)
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	defer pool.Stop()

	subnets := make(map[string]bool)
	for i := 0; i < 100; i++ {
		containerID := fmt.Sprintf("test-container-%d", i)
		configHash := fmt.Sprintf("hash-%d", i)

		result, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
		if err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}

		if subnets[result.Subnet] {
			t.Errorf("duplicate subnet allocated: %s", result.Subnet)
		}
		subnets[result.Subnet] = true
	}

	if len(subnets) != 100 {
		t.Errorf("expected 100 unique subnets, got %d", len(subnets))
	}

	for i := 0; i < 100; i++ {
		containerID := fmt.Sprintf("test-container-%d", i)

		pool.state.mu.RLock()
		var networkName string
		for name, entry := range pool.state.Networks {
			if entry.CurrentContainer != nil && *entry.CurrentContainer == containerID {
				networkName = name
				break
			}
		}
		pool.state.mu.RUnlock()

		if networkName != "" {
			pool.Release(ctx, containerID, networkName, true)
		}
	}
}

func TestSubnetUtilizationWarning(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")
	ctx := context.Background()

	config := SubnetConfig{
		BaseIP:     "10.240.0.0",
		SubnetMask: 20,
		MaxSubnets: 16,
	}

	var loggedWarnings []string
	logger := slog.New(slog.NewTextHandler(&testWriter{
		writeFunc: func(p []byte) (int, error) {
			msg := string(p)
			if contains(msg, "high subnet utilization") {
				loggedWarnings = append(loggedWarnings, msg)
			}
			return len(p), nil
		},
	}, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pool, err := NewWithConfig(ctx, stateFile, config, logger)
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	defer pool.Stop()

	for i := 0; i < 14; i++ {
		containerID := fmt.Sprintf("test-warn-%d", i)
		configHash := fmt.Sprintf("hash-warn-%d", i)

		_, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
		if err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	if len(loggedWarnings) == 0 {
		t.Error("expected warning to be logged for high subnet utilization")
	}

	for i := 0; i < 14; i++ {
		containerID := fmt.Sprintf("test-warn-%d", i)

		pool.state.mu.RLock()
		var networkName string
		for name, entry := range pool.state.Networks {
			if entry.CurrentContainer != nil && *entry.CurrentContainer == containerID {
				networkName = name
				break
			}
		}
		pool.state.mu.RUnlock()

		if networkName != "" {
			pool.Release(ctx, containerID, networkName, true)
		}
	}
}

func TestStatsWithSubnetUtilization(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "test_state.json")
	ctx := context.Background()

	config := SubnetConfig{
		BaseIP:     "10.250.0.0",
		SubnetMask: 20,
		MaxSubnets: 16,
	}

	pool, err := NewWithConfig(ctx, stateFile, config, slog.Default())
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	defer pool.Stop()

	stats := pool.Stats()
	if stats.MaxSubnets != 16 {
		t.Errorf("MaxSubnets = %d, want 16", stats.MaxSubnets)
	}
	if stats.SubnetUtilization != 0 {
		t.Errorf("SubnetUtilization = %f, want 0", stats.SubnetUtilization)
	}

	containerID := "test-stats-util"
	configHash := "test-hash-stats"

	result, err := pool.Acquire(ctx, containerID, configHash, nil, nil)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	stats = pool.Stats()
	if stats.SubnetUtilization < 0.06 || stats.SubnetUtilization > 0.07 {
		t.Errorf("SubnetUtilization = %f, want ~0.0625 (1/16)", stats.SubnetUtilization)
	}

	pool.Release(ctx, containerID, result.NetworkName, true)
}

type testWriter struct {
	writeFunc func([]byte) (int, error)
}

func (w *testWriter) Write(p []byte) (int, error) {
	if w.writeFunc != nil {
		return w.writeFunc(p)
	}
	return len(p), nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func parseIP(s string) []byte {
	var a, b, c, d byte
	fmt.Sscanf(s, "%d.%d.%d.%d", &a, &b, &c, &d)
	return []byte{a, b, c, d}
}
