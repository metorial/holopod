package container

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	registryTypes "github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/bastion"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/config"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/jsonmsg"
)

type Manager struct {
	docker            *client.Client
	containerID       string
	containerName     string
	networkName       string
	config            *config.Config
	networkViaBastion bool
	earlyExitCode     *int // Set if container exits before network setup
}

func NewManager(containerName, networkName string, cfg *config.Config) (*Manager, error) {
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker not available: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := docker.Ping(ctx); err != nil {
		return nil, fmt.Errorf("docker not available: %w", err)
	}

	return &Manager{
		docker:            docker,
		containerName:     containerName,
		networkName:       networkName,
		config:            cfg,
		networkViaBastion: false,
	}, nil
}

func (m *Manager) Docker() *client.Client {
	return m.docker
}

func (m *Manager) ContainerID() string {
	return m.containerID
}

func (m *Manager) NetworkName() string {
	return m.networkName
}

func (m *Manager) CheckGVisor(ctx context.Context) error {
	info, err := m.docker.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to get Docker info: %w", err)
	}

	if info.Runtimes == nil {
		return fmt.Errorf("no runtimes available in Docker daemon")
	}

	if _, ok := info.Runtimes[m.config.Container.Runtime]; !ok {
		return fmt.Errorf("runtime '%s' not found in Docker daemon", m.config.Container.Runtime)
	}

	// jsonmsg.Info(fmt.Sprintf("gVisor runtime '%s' is available", m.config.Container.Runtime))
	jsonmsg.Info("Setting up Holopod runtime")

	return nil
}

func (m *Manager) SetupNetworkViaBastion(ctx context.Context, subnet *string, bastionClient *bastion.Client) error {
	// jsonmsg.Info(fmt.Sprintf("Setting up network via bastion pool: %s", m.networkName))
	jsonmsg.Info("Setting up Holopod networking")

	if m.networkName == "" || m.networkName == "bridge" {
		m.networkName = "bridge"
		m.networkViaBastion = false
		jsonmsg.Info("Using default Docker bridge network")
		return nil
	}

	jsonmsg.Warning(fmt.Sprintf("Network '%s' is not supported; forcing default Docker bridge network", m.networkName))
	m.networkName = "bridge"
	m.networkViaBastion = false
	return nil
}

func (m *Manager) CleanupNetwork(ctx context.Context, bastionClient *bastion.Client) error {
	if !m.networkViaBastion {
		return nil
	}

	// jsonmsg.Info(fmt.Sprintf("Releasing network %s to bastion pool", m.networkName))
	return bastionClient.ReleaseNetwork(m.networkName, false)
}

// encodeAuthConfig encodes credentials for Docker API
func encodeAuthConfig(authConfig registryTypes.AuthConfig) (string, error) {
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		return "", err
	}

	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	// SECURITY: Clear JSON bytes
	for i := range encodedJSON {
		encodedJSON[i] = 0
	}

	return authStr, nil
}

// sanitizeDockerError removes credentials from error messages
func sanitizeDockerError(errMsg string) string {
	errMsg = regexp.MustCompile(`(https?://)[^:]+:[^@]+@`).ReplaceAllString(errMsg, "${1}***:***@")
	errMsg = regexp.MustCompile(`auth=[A-Za-z0-9+/=]+`).ReplaceAllString(errMsg, "auth=***")
	errMsg = regexp.MustCompile(`(?i)authorization:\s*[^\s]+`).ReplaceAllString(errMsg, "authorization: ***")
	return errMsg
}

func (m *Manager) PullImage(ctx context.Context, imageRef string, auth *config.ImageAuth) error {
	// Check if image exists locally
	_, _, err := m.docker.ImageInspectWithRaw(ctx, imageRef)
	if err == nil {
		// jsonmsg.Info("Image already exists locally")
		jsonmsg.ImagePullCompleted(imageRef, "registry-1.docker.io", true)
		return nil
	}

	if !client.IsErrNotFound(err) {
		return fmt.Errorf("failed to inspect image: %w", err)
	}

	jsonmsg.Info("Image not found locally, pulling from registry...")

	// Determine registry for event
	registry := "registry-1.docker.io"
	authenticated := false
	if auth != nil && auth.Type == "basic" {
		authenticated = true
	}

	// Emit image pull started event
	jsonmsg.ImagePullStarted(imageRef, registry, authenticated)

	// Build pull options with authentication
	pullOptions := image.PullOptions{}

	if auth != nil && auth.Type == "basic" {
		authConfig := registryTypes.AuthConfig{
			Username: auth.Username,
			Password: auth.Password,
		}

		encodedAuth, err := encodeAuthConfig(authConfig)
		if err != nil {
			return fmt.Errorf("failed to encode auth: %w", err)
		}

		pullOptions.RegistryAuth = encodedAuth

		// SECURITY: Clear auth immediately
		authConfig.Username = ""
		authConfig.Password = ""

		jsonmsg.Info("Pulling with authentication...")
	} else {
		jsonmsg.Info("Pulling without authentication...")
	}

	out, err := m.docker.ImagePull(ctx, imageRef, pullOptions)
	if err != nil {
		errMsg := sanitizeDockerError(err.Error())
		return fmt.Errorf("failed to pull image: %s", errMsg)
	}
	defer out.Close()

	// Stream pull progress
	scanner := bufio.NewScanner(out)
	lastStatus := ""
	for scanner.Scan() {
		var pullEvent struct {
			Status         string `json:"status"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
			Progress string `json:"progress"`
			Error    string `json:"error"`
		}

		if err := json.Unmarshal(scanner.Bytes(), &pullEvent); err == nil {
			if pullEvent.Error != "" {
				errMsg := sanitizeDockerError(pullEvent.Error)
				return fmt.Errorf("image pull failed: %s", errMsg)
			}

			if pullEvent.Status != lastStatus && pullEvent.Status != "" {
				jsonmsg.Info(fmt.Sprintf("Pull: %s", pullEvent.Status))
				lastStatus = pullEvent.Status
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read pull response: %w", err)
	}

	jsonmsg.Info("Successfully pulled image")
	jsonmsg.ImagePullCompleted(imageRef, registry, false)
	return nil
}

func (m *Manager) CreateContainer(ctx context.Context, imageRef string, cmd []string, args []string, auth *config.ImageAuth) error {
	jsonmsg.Info(fmt.Sprintf("Creating Holopod instance: %s", m.containerName))

	if err := config.ValidateImageReference(imageRef); err != nil {
		return err
	}

	// Pull image with authentication
	if err := m.PullImage(ctx, imageRef, auth); err != nil {
		return err
	}

	if err := config.ValidateEnvironmentVariables(m.config.Container.Environment); err != nil {
		return err
	}

	hostConfig := &container.HostConfig{
		Runtime:     m.config.Container.Runtime,
		NetworkMode: container.NetworkMode(m.networkName),
		AutoRemove:  m.config.Execution.AutoCleanup, // Auto-remove when container exits normally
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
	}

	if m.config.Container.MemoryLimit != nil {
		mem, err := parseMemoryLimit(*m.config.Container.MemoryLimit)
		if err != nil {
			return err
		}
		hostConfig.Memory = mem
	}

	if m.config.Container.CPULimit != nil {
		nano, err := parseCPULimit(*m.config.Container.CPULimit)
		if err != nil {
			return err
		}
		hostConfig.NanoCPUs = nano
	}

	if len(m.config.Container.Tmpfs) > 0 {
		hostConfig.Tmpfs = make(map[string]string)
		for _, path := range m.config.Container.Tmpfs {
			hostConfig.Tmpfs[path] = ""
		}
	}

	if m.config.Container.ReadonlyRootfs {
		hostConfig.ReadonlyRootfs = true
		if hostConfig.Tmpfs == nil {
			hostConfig.Tmpfs = make(map[string]string)
		}
		hostConfig.Tmpfs["/tmp"] = "rw,noexec,nosuid,size=100m"
		jsonmsg.Info("Readonly rootfs enabled with writable /tmp")
	}

	// Configure DNS servers if provided
	// If empty, Docker will use its default DNS (127.0.0.11 or host's /etc/resolv.conf)
	if len(m.config.Network.DNSServers) > 0 {
		hostConfig.DNS = m.config.Network.DNSServers
		jsonmsg.Info(fmt.Sprintf("Using custom DNS servers: %v", m.config.Network.DNSServers))
	}

	// Add labels for container tracking and orphan cleanup
	labels := map[string]string{
		"managed-by":         "isolation-runner",
		"isolation-runner":   "true",
		"container-name":     m.containerName,
		"creation-timestamp": fmt.Sprintf("%d", time.Now().Unix()),
	}

	containerConfig := &container.Config{
		Image:        imageRef,
		Hostname:     m.containerName,
		AttachStdin:  m.config.Execution.AttachStdin,
		AttachStdout: m.config.Execution.AttachStdout,
		AttachStderr: m.config.Execution.AttachStderr,
		Tty:          m.config.Execution.TTY,
		OpenStdin:    m.config.Execution.Interactive,
		Labels:       labels,
	}

	// Docker container semantics:
	// - Entrypoint: The command to run (overrides image's ENTRYPOINT)
	// - Cmd: Arguments to the Entrypoint (overrides image's CMD)
	// If only args are provided (no command), they are passed to the image's default Entrypoint
	// If only command is provided (no args), it runs without arguments
	// If both are provided, command runs with args
	if len(cmd) > 0 {
		containerConfig.Entrypoint = cmd
	}
	if len(args) > 0 {
		containerConfig.Cmd = args
	}

	if len(m.config.Container.Environment) > 0 {
		env := make([]string, 0, len(m.config.Container.Environment))
		for k, v := range m.config.Container.Environment {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		containerConfig.Env = env
	}

	if m.config.Container.WorkingDir != nil {
		containerConfig.WorkingDir = *m.config.Container.WorkingDir
	}

	resp, err := m.docker.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, m.containerName)
	if err != nil {
		errMsg := sanitizeDockerError(err.Error())
		return fmt.Errorf("failed to create container: %s", errMsg)
	}

	m.containerID = resp.ID
	// jsonmsg.Info(fmt.Sprintf("Container created with ID: %s", resp.ID))
	jsonmsg.Info("Holopod instance created successfully")
	jsonmsg.ContainerCreated(resp.ID, m.containerName, imageRef)

	for _, warning := range resp.Warnings {
		jsonmsg.Warning(fmt.Sprintf("Container creation warning: %s", warning))
	}

	return nil
}

func (m *Manager) StartContainer(ctx context.Context) error {
	if m.containerID == "" {
		return fmt.Errorf("container not created")
	}

	// jsonmsg.Info(fmt.Sprintf("Starting container: %s", m.containerID))
	jsonmsg.Info("Starting Holopod instance")

	if err := m.docker.ContainerStart(ctx, m.containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Inspect to get PID
	inspect, err := m.docker.ContainerInspect(ctx, m.containerID)
	if err == nil && inspect.State != nil && inspect.State.Pid != 0 {
		jsonmsg.ContainerStarted(m.containerID, m.containerName, inspect.State.Pid)
	}

	jsonmsg.Info("Holopod instance started successfully")
	return nil
}

func (m *Manager) AttachStreams(ctx context.Context) error {
	if m.containerID == "" {
		return fmt.Errorf("container not created")
	}

	if !m.config.Execution.AttachStdout && !m.config.Execution.AttachStderr {
		return nil
	}

	resp, err := m.docker.ContainerAttach(ctx, m.containerID, container.AttachOptions{
		Stream: true,
		Stdout: m.config.Execution.AttachStdout,
		Stderr: m.config.Execution.AttachStderr,
		Logs:   true,
	})
	if err != nil {
		return fmt.Errorf("failed to attach to container: %w", err)
	}

	// Create custom writers that emit JSON immediately for each write
	stdoutWriter := &jsonStreamWriter{streamType: "stdout"}
	stderrWriter := &jsonStreamWriter{streamType: "stderr"}

	go func() {
		defer resp.Close()
		_, _ = stdcopy.StdCopy(stdoutWriter, stderrWriter, resp.Reader)
	}()

	return nil
}

// jsonStreamWriter is a custom io.Writer that emits JSON messages for Docker output
type jsonStreamWriter struct {
	streamType string
}

func (w *jsonStreamWriter) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	text := string(p)
	if w.streamType == "stdout" {
		jsonmsg.ContainerStdout(text)
	} else {
		jsonmsg.ContainerStderr(text)
	}

	return len(p), nil
}

func (m *Manager) RetrieveContainerLogs(ctx context.Context) (string, error) {
	if m.containerID == "" {
		return "", fmt.Errorf("container not created")
	}

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "50",
	}

	logs, err := m.docker.ContainerLogs(ctx, m.containerID, options)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve logs: %w", err)
	}
	defer logs.Close()

	var stdout, stderr strings.Builder
	_, _ = stdcopy.StdCopy(&stdout, &stderr, logs)

	combined := stdout.String()
	if stderr.Len() > 0 {
		if combined != "" {
			combined += "\n"
		}
		combined += stderr.String()
	}

	return combined, nil
}

func (m *Manager) GetContainerIP(ctx context.Context) (net.IP, error) {
	if m.containerID == "" {
		return nil, fmt.Errorf("container not created")
	}

	for attempt := 1; attempt <= 10; attempt++ {
		inspect, err := m.docker.ContainerInspect(ctx, m.containerID)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect container: %w", err)
		}

		if inspect.State != nil && !inspect.State.Running {
			exitCode := inspect.State.ExitCode
			errMsg := inspect.State.Error

			// Store exit code so WaitForExit can return it
			m.earlyExitCode = &exitCode

			// For containers that complete successfully before network setup, this is normal
			if exitCode == 0 {
				// jsonmsg.Info("Container completed successfully before network isolation could be configured (short-running container)")
				return nil, fmt.Errorf("container completed before network setup (exit code: 0)")
			}

			// For failed containers, log errors
			logs, _ := m.RetrieveContainerLogs(ctx)
			if logs != "" {
				jsonmsg.Error(fmt.Sprintf("Holopod instance logs:\n%s", logs))
			}

			jsonmsg.Error(fmt.Sprintf("Holopod instance exited early with code %d", exitCode))
			if errMsg != "" {
				jsonmsg.Error(fmt.Sprintf("Holopod instance error: %s", errMsg))
			}

			return nil, fmt.Errorf("container exited before IP assignment (exit code: %d)",
				exitCode)
		}

		if inspect.NetworkSettings == nil || inspect.NetworkSettings.Networks == nil {
			return nil, fmt.Errorf("no network settings found")
		}

		netInfo, ok := inspect.NetworkSettings.Networks[m.networkName]
		if !ok {
			return nil, fmt.Errorf("network %s not found", m.networkName)
		}

		if netInfo.IPAddress != "" {
			ip := net.ParseIP(netInfo.IPAddress)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP address: %s", netInfo.IPAddress)
			}
			// jsonmsg.Info(fmt.Sprintf("Container IP address: %s", ip.String()))
			jsonmsg.ContainerIPReady(m.containerID, ip.String(), m.networkName)
			return ip, nil
		}

		if attempt < 10 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	return nil, fmt.Errorf("no IP address assigned after 10 attempts")
}

func (m *Manager) WaitForExit(ctx context.Context) (int, error) {
	if m.containerID == "" {
		return -1, fmt.Errorf("container not created")
	}

	// If we already captured the exit code during early exit detection, return it
	if m.earlyExitCode != nil {
		return *m.earlyExitCode, nil
	}

	statusCh, errCh := m.docker.ContainerWait(ctx, m.containerID, container.WaitConditionNotRunning)

	select {
	case err := <-errCh:
		if err != nil {
			return -1, fmt.Errorf("error waiting for container: %w", err)
		}
	case status := <-statusCh:
		return int(status.StatusCode), nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}

	return -1, fmt.Errorf("unexpected wait exit")
}

func (m *Manager) StopContainer(ctx context.Context, timeout int) error {
	if m.containerID == "" {
		return nil
	}

	// jsonmsg.Info(fmt.Sprintf("Stopping container: %s", m.containerID))
	jsonmsg.ContainerTerminating(m.containerID, "stop_requested", false)

	stopTimeout := timeout
	if err := m.docker.ContainerStop(ctx, m.containerID, container.StopOptions{
		Timeout: &stopTimeout,
	}); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	return nil
}

func (m *Manager) RemoveContainer(ctx context.Context) error {
	if m.containerID == "" {
		return nil
	}

	// jsonmsg.Info(fmt.Sprintf("Removing container: %s", m.containerID))

	if err := m.docker.ContainerRemove(ctx, m.containerID, container.RemoveOptions{
		Force: true,
	}); err != nil {
		// If container is already gone or being removed (e.g., due to AutoCleanup), that's fine
		errStr := err.Error()
		if strings.Contains(errStr, "No such container") ||
			strings.Contains(errStr, "removal of container") && strings.Contains(errStr, "is already in progress") {
			// jsonmsg.Info("Container already removed or removal in progress")
			return nil
		}
		return fmt.Errorf("failed to remove container: %w", err)
	}

	return nil
}

func parseMemoryLimit(limit string) (int64, error) {
	limit = strings.TrimSpace(strings.ToLower(limit))

	multiplier := int64(1)
	if strings.HasSuffix(limit, "k") {
		multiplier = 1024
		limit = limit[:len(limit)-1]
	} else if strings.HasSuffix(limit, "m") {
		multiplier = 1024 * 1024
		limit = limit[:len(limit)-1]
	} else if strings.HasSuffix(limit, "g") {
		multiplier = 1024 * 1024 * 1024
		limit = limit[:len(limit)-1]
	}

	value, err := strconv.ParseInt(limit, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory limit: %s", limit)
	}

	bytes := value * multiplier

	const minMemory = 4 * 1024 * 1024
	const maxMemory = 128 * 1024 * 1024 * 1024

	if bytes < minMemory {
		return 0, fmt.Errorf("memory limit too low: %d bytes (minimum: 4MB)", bytes)
	}
	if bytes > maxMemory {
		return 0, fmt.Errorf("memory limit too high: %d bytes (maximum: 128GB)", bytes)
	}

	return bytes, nil
}

func parseCPULimit(limit string) (int64, error) {
	limit = strings.TrimSpace(limit)

	value, err := strconv.ParseFloat(limit, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid CPU limit: %s", limit)
	}

	const minCPU = 0.01
	const maxCPU = 256.0

	if value < minCPU {
		return 0, fmt.Errorf("CPU limit too low: %.2f (minimum: 0.01)", value)
	}
	if value > maxCPU {
		return 0, fmt.Errorf("CPU limit too high: %.2f (maximum: 256)", value)
	}

	return int64(value * 1e9), nil
}
