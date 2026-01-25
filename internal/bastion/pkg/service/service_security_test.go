package service

import (
	"context"
	"log/slog"
	"net"
	"os"
	"testing"

	"github.com/metorial/fleet/holopod/internal/bastion/pkg/networkpool"
	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
)

func TestChainIPTrackingUnit(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	tmpDir := t.TempDir()
	ctx := context.Background()
	pool, err := networkpool.New(ctx, tmpDir+"/test_state.json")
	if err != nil {
		t.Skip("Docker not available")
	}
	defer pool.Stop()

	server := New("test", pool, logger)

	t.Run("stores IP mapping", func(t *testing.T) {
		chainName := "ISO-test123456789ab"
		containerIP := "172.20.1.100"

		server.chainMu.Lock()
		server.chainIPs[chainName] = containerIP
		server.chainMu.Unlock()

		server.chainMu.RLock()
		storedIP := server.chainIPs[chainName]
		server.chainMu.RUnlock()

		if storedIP != containerIP {
			t.Errorf("stored IP = %s, want %s", storedIP, containerIP)
		}
	})

	t.Run("removes IP mapping", func(t *testing.T) {
		chainName := "ISO-test234567890ab"
		containerIP := "172.20.1.101"

		server.chainMu.Lock()
		server.chainIPs[chainName] = containerIP
		server.chainMu.Unlock()

		server.chainMu.Lock()
		delete(server.chainIPs, chainName)
		server.chainMu.Unlock()

		server.chainMu.RLock()
		_, exists := server.chainIPs[chainName]
		server.chainMu.RUnlock()

		if exists {
			t.Error("IP mapping should be removed")
		}
	})

	t.Run("concurrent access is thread-safe", func(t *testing.T) {
		done := make(chan bool)

		for i := 0; i < 100; i++ {
			go func(idx int) {
				chainName := "ISO-" + string(rune('a'+(idx%26))) + "1234567890abc"
				containerIP := "172.20.1." + string(rune('1'+(idx%254)))

				server.chainMu.Lock()
				server.chainIPs[chainName] = containerIP
				server.chainMu.Unlock()

				server.chainMu.RLock()
				_ = server.chainIPs[chainName]
				server.chainMu.RUnlock()

				server.chainMu.Lock()
				delete(server.chainIPs, chainName)
				server.chainMu.Unlock()

				done <- true
			}(i)
		}

		for i := 0; i < 100; i++ {
			<-done
		}
	})
}

func TestChainIPTracking(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	tmpDir := t.TempDir()
	ctx := context.Background()
	pool, err := networkpool.New(ctx, tmpDir+"/test_state.json")
	if err != nil {
		t.Skip("Docker not available")
	}
	defer pool.Stop()

	server := New("test", pool, logger)

	t.Run("stores and retrieves container IP", func(t *testing.T) {
		if os.Getuid() != 0 {
			t.Skip("skipping test; requires root for iptables")
		}

		chainName := "ISO-0123456789abcdef"
		containerIP := "172.20.1.100"
		containerID := "test-container-123"

		setupReq := &pb.SetupChainRequest{
			ChainName:   chainName,
			ContainerIp: containerIP,
			ContainerId: containerID,
		}

		resp, err := server.SetupChain(ctx, setupReq)
		if err != nil {
			t.Fatalf("SetupChain() error = %v", err)
		}
		if !resp.Success {
			t.Fatalf("SetupChain() not successful: %v", resp.GetError())
		}

		server.chainMu.RLock()
		storedIP := server.chainIPs[chainName]
		server.chainMu.RUnlock()

		if storedIP != containerIP {
			t.Errorf("stored IP = %s, want %s", storedIP, containerIP)
		}

		cleanupReq := &pb.CleanupChainRequest{
			ChainName:   chainName,
			ContainerId: containerID,
		}

		if _, err := server.CleanupChain(ctx, cleanupReq); err != nil {
			t.Logf("CleanupChain() error = %v", err)
		}

		server.chainMu.RLock()
		_, exists := server.chainIPs[chainName]
		server.chainMu.RUnlock()

		if exists {
			t.Error("chain IP should be removed after cleanup")
		}
	})

	t.Run("handles cleanup without stored IP", func(t *testing.T) {
		chainName := "ISO-1234567890abcdef"
		containerID := "test-container-456"

		cleanupReq := &pb.CleanupChainRequest{
			ChainName:   chainName,
			ContainerId: containerID,
		}

		if _, err := server.CleanupChain(ctx, cleanupReq); err != nil {
			t.Logf("CleanupChain() error = %v (expected if iptables not available)", err)
		}
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		done := make(chan bool)

		for i := 0; i < 10; i++ {
			go func(idx int) {
				chainName := "ISO-" + string(rune('0'+idx)) + "123456789abcd"
				containerIP := net.ParseIP("172.20.1." + string(rune('1'+idx)))

				setupReq := &pb.SetupChainRequest{
					ChainName:   chainName,
					ContainerIp: containerIP.String(),
					ContainerId: "test-" + string(rune('0'+idx)),
				}

				server.SetupChain(ctx, setupReq)

				cleanupReq := &pb.CleanupChainRequest{
					ChainName:   chainName,
					ContainerId: "test-" + string(rune('0'+idx)),
				}

				server.CleanupChain(ctx, cleanupReq)

				done <- true
			}(i)
		}

		for i := 0; i < 10; i++ {
			<-done
		}
	})
}
