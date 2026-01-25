package manager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/metorial/fleet/holopod/services/container-manager/pkg/container"
	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
)

const (
	CleanupIntervalSecs  = 300 // 5 minutes
	DefaultMaxContainers = 1000
)

type Manager struct {
	containers          map[string]*container.Container
	mu                  sync.RWMutex
	isolationRunnerPath string
	maxContainers       int
	cleanupStop         chan struct{}
	cleanupDone         chan struct{}
}

func New() (*Manager, error) {
	isolationRunnerPath, err := findIsolationRunner()
	if err != nil {
		return nil, fmt.Errorf("failed to find isolation-runner: %w", err)
	}

	maxContainers := DefaultMaxContainers
	if envVal := os.Getenv("MAX_CONTAINERS_PER_MANAGER"); envVal != "" {
		fmt.Sscanf(envVal, "%d", &maxContainers)
	}

	m := &Manager{
		containers:          make(map[string]*container.Container),
		isolationRunnerPath: isolationRunnerPath,
		maxContainers:       maxContainers,
		cleanupStop:         make(chan struct{}),
		cleanupDone:         make(chan struct{}),
	}

	go m.cleanupTask()

	return m, nil
}

func findIsolationRunner() (string, error) {
	if path := os.Getenv("ISOLATION_RUNNER_PATH"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	searchPaths := []string{
		"./target/debug/isolation-runner",
		"./target/release/isolation-runner",
		"../internal/isolation-runner/target/debug/isolation-runner",
		"../../internal/isolation-runner/target/debug/isolation-runner",
		"../../internal/isolation-runner/bin/isolation-runner",
		"../../../internal/isolation-runner/bin/isolation-runner",
		"/usr/local/bin/isolation-runner",
	}

	for _, path := range searchPaths {
		if absPath, err := filepath.Abs(path); err == nil {
			if _, err := os.Stat(absPath); err == nil {
				return absPath, nil
			}
		}
	}

	path, err := exec.LookPath("isolation-runner")
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf("isolation-runner not found in any search path")
}

func (m *Manager) CreateContainer(ctx context.Context, containerID string, config *pb.ContainerConfig) (string, error) {
	if containerID == "" {
		// Generate UUID without dashes (bastion requires hex-only)
		containerID = strings.ReplaceAll(uuid.New().String(), "-", "")
	}

	m.mu.Lock()
	if len(m.containers) >= m.maxContainers {
		m.mu.Unlock()
		return "", fmt.Errorf("maximum container limit reached (%d)", m.maxContainers)
	}

	if _, exists := m.containers[containerID]; exists {
		m.mu.Unlock()
		return "", fmt.Errorf("container with ID %s already exists", containerID)
	}

	c := container.New(containerID, config)
	m.containers[containerID] = c
	m.mu.Unlock()

	if err := c.Start(m.isolationRunnerPath); err != nil {
		m.mu.Lock()
		delete(m.containers, containerID)
		m.mu.Unlock()
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	return containerID, nil
}

func (m *Manager) GetContainer(containerID string) (*container.Container, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, exists := m.containers[containerID]
	if !exists {
		return nil, fmt.Errorf("container not found: %s", containerID)
	}

	return c, nil
}

func (m *Manager) ListContainers(filter string) []*pb.ContainerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	containers := make([]*pb.ContainerInfo, 0, len(m.containers))

	for id, c := range m.containers {
		state := c.GetState()

		include := false
		switch filter {
		case "running":
			include = state.State == pb.ContainerState_RUNNING
		case "exited":
			include = state.State == pb.ContainerState_EXITED ||
				state.State == pb.ContainerState_FAILED ||
				state.State == pb.ContainerState_TERMINATED
		case "all", "":
			include = true
		default:
			include = true
		}

		if include {
			info := &pb.ContainerInfo{
				ContainerId: id,
				State:       state.State,
				CreatedAt:   state.CreatedAt,
				FinishedAt:  state.FinishedAt,
				ExitCode:    state.ExitCode,
			}
			if state.Config != nil {
				// Extract image display name from ImageSpec
				if state.Config.ImageSpec != nil {
					registry := state.Config.ImageSpec.GetRegistry()
					if registry == "" || registry == "registry-1.docker.io" {
						info.Image = state.Config.ImageSpec.Image
					} else {
						info.Image = fmt.Sprintf("%s/%s", registry, state.Config.ImageSpec.Image)
					}
				}
				info.Command = state.Config.Command
			}
			containers = append(containers, info)
		}
	}

	return containers
}

func (m *Manager) TerminateContainer(containerID string, force bool, timeoutSecs uint32) error {
	c, err := m.GetContainer(containerID)
	if err != nil {
		return err
	}

	return c.Terminate(force, timeoutSecs)
}

func (m *Manager) WaitContainer(containerID string, timeoutSecs uint32) (int32, error) {
	c, err := m.GetContainer(containerID)
	if err != nil {
		return 0, err
	}

	return c.Wait(timeoutSecs)
}

func (m *Manager) GetContainerStatus(containerID string) (*pb.ContainerStatus, error) {
	c, err := m.GetContainer(containerID)
	if err != nil {
		return nil, err
	}

	return c.GetState(), nil
}

func (m *Manager) SubscribeStdout(containerID string) <-chan []byte {
	c, err := m.GetContainer(containerID)
	if err != nil {
		ch := make(chan []byte)
		close(ch)
		return ch
	}

	return c.SubscribeStdout()
}

func (m *Manager) SubscribeStderr(containerID string) <-chan []byte {
	c, err := m.GetContainer(containerID)
	if err != nil {
		ch := make(chan []byte)
		close(ch)
		return ch
	}

	return c.SubscribeStderr()
}

func (m *Manager) SubscribeMessages(containerID string) <-chan string {
	c, err := m.GetContainer(containerID)
	if err != nil {
		ch := make(chan string)
		close(ch)
		return ch
	}

	return c.SubscribeMessages()
}

func (m *Manager) WriteStdin(containerID string, data []byte) error {
	c, err := m.GetContainer(containerID)
	if err != nil {
		return err
	}

	return c.WriteStdin(data)
}

func (m *Manager) cleanupTask() {
	ticker := time.NewTicker(time.Duration(CleanupIntervalSecs) * time.Second)
	defer ticker.Stop()
	defer close(m.cleanupDone)

	for {
		select {
		case <-ticker.C:
			m.cleanupExitedContainers()
		case <-m.cleanupStop:
			return
		}
	}
}

func (m *Manager) cleanupExitedContainers() {
	now := time.Now().Unix()

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, c := range m.containers {
		state := c.GetState()

		if state.CleanupAfter != nil && now >= *state.CleanupAfter {
			c.Close()
			delete(m.containers, id)
		}
	}
}

func (m *Manager) CleanupExitedContainersNow() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for id, c := range m.containers {
		state := c.GetState()

		if state.State == pb.ContainerState_EXITED ||
			state.State == pb.ContainerState_FAILED ||
			state.State == pb.ContainerState_TERMINATED {
			c.Close()
			delete(m.containers, id)
			count++
		}
	}

	return count
}

func (m *Manager) GetStats() (int, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalContainers := len(m.containers)
	runningContainers := 0

	for _, c := range m.containers {
		state := c.GetState()
		if state.State == pb.ContainerState_RUNNING {
			runningContainers++
		}
	}

	return totalContainers, runningContainers
}

func (m *Manager) Stop() {
	close(m.cleanupStop)
	<-m.cleanupDone

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, c := range m.containers {
		c.Close()
	}
}
