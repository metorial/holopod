package lifecycle

import (
	"context"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/bastion"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/config"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/jsonmsg"
)

type ResourceTracker struct {
	docker    *client.Client
	mu        sync.Mutex
	resources trackedResources
}

type trackedResources struct {
	containerID       string
	containerName     string
	networkName       string
	networkViaBastion bool
	chainName         string
}

func NewResourceTracker(docker *client.Client) *ResourceTracker {
	return &ResourceTracker{
		docker: docker,
	}
}

func (t *ResourceTracker) TrackContainer(containerID, containerName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resources.containerID = containerID
	t.resources.containerName = containerName
}

func (t *ResourceTracker) TrackNetwork(networkName string, viaBastion bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resources.networkName = networkName
	t.resources.networkViaBastion = viaBastion
}

func (t *ResourceTracker) TrackChain(chainName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resources.chainName = chainName
}

func (t *ResourceTracker) UntrackContainer() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resources.containerID = ""
	t.resources.containerName = ""
}

func (t *ResourceTracker) UntrackNetwork() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resources.networkName = ""
}

func (t *ResourceTracker) UntrackChain() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resources.chainName = ""
}

func (t *ResourceTracker) CleanupAll(ctx context.Context) {
	t.mu.Lock()
	resources := t.resources
	t.mu.Unlock()

	if resources.containerID != "" {
		t.cleanupContainer(ctx, resources.containerID)
	}

	if resources.networkName != "" {
		t.cleanupNetwork(ctx, resources.networkName, resources.networkViaBastion, resources.containerName)
	}

	if resources.chainName != "" {
		t.cleanupChain(ctx, resources.chainName)
	}
}

func (t *ResourceTracker) cleanupContainer(ctx context.Context, containerID string) {
	// jsonmsg.Info("Cleaning up tracked container: " + containerID)

	timeout := 5
	_ = t.docker.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: &timeout,
	})

	if err := t.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: true,
	}); err != nil {
		if !client.IsErrNotFound(err) {
			// jsonmsg.Warning("Error removing container: " + err.Error())
		}
	}
}

func (t *ResourceTracker) cleanupNetwork(ctx context.Context, networkName string, viaBastion bool, containerName string) {
	if !viaBastion {
		return
	}

	// jsonmsg.Info("Releasing tracked network via bastion: " + networkName)

	bastionAddress := config.GetBastionAddress()
	bastionClient, err := bastion.Connect(bastionAddress, containerName)
	if err != nil {
		jsonmsg.Warning("Could not connect to bastion for network cleanup: " + err.Error())
		return
	}
	defer bastionClient.Close()

	if err := bastionClient.ReleaseNetwork(networkName, false); err != nil {
		jsonmsg.Warning("Failed to release network via bastion: " + err.Error())
	}
}

func (t *ResourceTracker) cleanupChain(ctx context.Context, chainName string) {
	// jsonmsg.Info("Cleaning up tracked iptables chain: " + chainName)

	bastionAddress := config.GetBastionAddress()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	bastionClient, err := bastion.Connect(bastionAddress, "cleanup")
	if err != nil {
		jsonmsg.Warning("Could not connect to bastion for chain cleanup: " + err.Error())
		return
	}
	defer bastionClient.Close()

	if err := bastionClient.CleanupChain(chainName); err != nil {
		jsonmsg.Warning("Failed to cleanup chain via bastion: " + err.Error())
	}
}
