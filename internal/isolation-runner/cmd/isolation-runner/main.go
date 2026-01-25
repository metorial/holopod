package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/config"
	ierrors "github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/errors"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/jsonmsg"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/lifecycle"
)

const version = "1.2.0"

func main() {
	// CRITICAL: Ensure cleanup always runs, even on panic
	var tracker *lifecycle.ResourceTracker
	var exitCode int

	defer func() {
		if r := recover(); r != nil {
			jsonmsg.Error(fmt.Sprintf("PANIC: isolation-runner crashed: %v", r))
			exitCode = int(ierrors.ExitRuntimeError)
		}

		// ALWAYS cleanup resources, even on panic
		if tracker != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			jsonmsg.Info("Performing final resource cleanup...")
			tracker.CleanupAll(ctx)
		}

		if exitCode != 0 {
			jsonmsg.ContainerExit(exitCode)
		}
	}()

	exitCode, tracker = run()
	os.Exit(exitCode)
}

func run() (int, *lifecycle.ResourceTracker) {
	input, err := config.ReadInputFromStdin()
	if err != nil {
		jsonmsg.Error(fmt.Sprintf("Failed to read input: %v", err))
		jsonmsg.ContainerExit(1)
		return 1, nil
	}

	cfg := &input.Config

	jsonmsg.Info(fmt.Sprintf("Running on Metorial Holopod v%s", version))
	jsonmsg.Info(fmt.Sprintf("Image: %s", input.GetImageDisplayName()))
	// jsonmsg.Info(fmt.Sprintf("Container: %s", input.GetContainerName()))

	ctx := context.Background()
	startTime := time.Now()

	manager, err := lifecycle.SetupContainer(ctx, input, cfg)
	if err != nil {
		jsonmsg.Error(fmt.Sprintf("Failed to setup holopod instance: %v", err))
		exitCode := getExitCode(err)
		jsonmsg.ContainerExit(exitCode)
		// Emit structured event even for setup failures
		duration := time.Since(startTime)
		jsonmsg.ContainerExitedWithDetails("unknown", exitCode, duration.String())
		return exitCode, nil
	}

	tracker := lifecycle.NewResourceTracker(manager.Docker())

	initialNetwork := input.GetBridgeName()
	actualNetwork := manager.NetworkName()
	viaBastion := actualNetwork != initialNetwork

	tracker.TrackNetwork(actualNetwork, viaBastion)

	containerID := manager.ContainerID()
	tracker.TrackContainer(containerID, input.GetContainerName())

	if err := manager.StartContainer(ctx); err != nil {
		jsonmsg.Error(fmt.Sprintf("Failed to start holopod instance: %v", err))
		exitCode := getExitCode(err)
		jsonmsg.ContainerExit(exitCode)
		// Emit structured event for start failures
		duration := time.Since(startTime)
		jsonmsg.ContainerExitedWithDetails(containerID, exitCode, duration.String())
		return exitCode, tracker
	}

	if err := manager.AttachStreams(ctx); err != nil {
		jsonmsg.Warning(fmt.Sprintf("Failed to attach streams: %v", err))
	}

	if err := manager.StartStdinForwarder(ctx); err != nil {
		jsonmsg.Warning(fmt.Sprintf("Failed to start stdin forwarder: %v", err))
	}

	containerIP, err := manager.GetContainerIP(ctx)
	var chainName string
	if err != nil {
		// Check if container has already exited (common for short-running containers)
		if strings.Contains(err.Error(), "container completed before network setup") ||
			strings.Contains(err.Error(), "No such container") {
			// Skip network isolation setup, proceed to wait for exit code
			// Info message already logged by GetContainerIP
		} else {
			time.Sleep(150 * time.Millisecond)
			jsonmsg.Error(fmt.Sprintf("Failed to get holopod instance IP: %v", err))
			exitCode := getExitCode(err)
			jsonmsg.ContainerExit(exitCode)
			// Emit structured event for IP assignment failures
			duration := time.Since(startTime)
			jsonmsg.ContainerExitedWithDetails(containerID, exitCode, duration.String())
			return exitCode, tracker
		}
	} else {
		// Set up network isolation only if container is still running
		var setupErr error
		chainName, setupErr = lifecycle.SetupNetworkIsolation(ctx, containerID, containerIP.String(), cfg)
		if setupErr != nil {
			jsonmsg.Error(fmt.Sprintf("Failed to setup network isolation: %v", setupErr))
			exitCode := getExitCode(setupErr)
			jsonmsg.ContainerExit(exitCode)
			// Emit structured event for network isolation failures
			duration := time.Since(startTime)
			jsonmsg.ContainerExitedWithDetails(containerID, exitCode, duration.String())
			return exitCode, tracker
		}
		tracker.TrackChain(chainName)

		// Container is now fully ready (started + network isolation configured)
		if containerIP != nil {
			jsonmsg.ContainerReady(containerID, containerIP.String())
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		jsonmsg.Info("Received termination signal, stopping Holopod instance...")
		jsonmsg.ContainerTerminating(containerID, "termination_signal", false)
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		manager.StopContainer(stopCtx, 5)
	}()

	jsonmsg.Info("Waiting for Holopod instance to exit...")
	exitCode := 0
	code, err := manager.WaitForExit(ctx)
	if err != nil {
		jsonmsg.Warning(fmt.Sprintf("Error waiting for container: %v", err))
		exitCode = 1
	} else {
		exitCode = code
	}

	duration := time.Since(startTime)
	jsonmsg.Info(fmt.Sprintf("Holopod instance exited with code: %d", exitCode))
	jsonmsg.ContainerExitedWithDetails(containerID, exitCode, duration.String())

	// Only cleanup network isolation if it was set up
	if chainName != "" {
		lifecycle.CleanupNetworkIsolation(ctx, chainName)
		tracker.UntrackChain()
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := manager.RemoveContainer(cleanupCtx); err != nil {
		jsonmsg.Warning(fmt.Sprintf("Failed to remove container: %v", err))
	}
	tracker.UntrackContainer()

	tracker.UntrackNetwork()

	jsonmsg.Info(fmt.Sprintf("Holopod instance completed with exit code: %d", exitCode))
	jsonmsg.ContainerExit(exitCode)

	return exitCode, tracker
}

func getExitCode(err error) int {
	if err == nil {
		return 0
	}

	if ie, ok := err.(*ierrors.IsolationError); ok {
		return ie.ExitCode()
	}

	errStr := strings.ToLower(err.Error())

	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
		return int(ierrors.ExitTimeout)
	}

	if strings.Contains(errStr, "docker") || strings.Contains(errStr, "daemon") {
		return int(ierrors.ExitDockerError)
	}

	if strings.Contains(errStr, "gvisor") || strings.Contains(errStr, "runtime") {
		return int(ierrors.ExitSetupError)
	}

	if strings.Contains(errStr, "iptables") || strings.Contains(errStr, "network") || strings.Contains(errStr, "bastion") {
		return int(ierrors.ExitRuntimeError)
	}

	if strings.Contains(errStr, "config") || strings.Contains(errStr, "input") || strings.Contains(errStr, "validation") {
		return int(ierrors.ExitConfigError)
	}

	return int(ierrors.ExitConfigError)
}
