package container

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
	"google.golang.org/protobuf/proto"
)

type Container struct {
	ID               string
	Config           *pb.ContainerConfig
	cmd              *exec.Cmd
	state            *pb.ContainerStatus
	stateMu          sync.RWMutex
	stdoutBroadcast  chan []byte
	stderrBroadcast  chan []byte
	messageBroadcast chan string
	stdinWriter      io.WriteCloser
	exitCh           chan int32
	ctx              context.Context
	cancel           context.CancelFunc
	closeOnce        sync.Once
}

func New(id string, config *pb.ContainerConfig) *Container {
	ctx, cancel := context.WithCancel(context.Background())

	now := fmt.Sprintf("%d", time.Now().Unix())
	return &Container{
		ID:     id,
		Config: config,
		state: &pb.ContainerStatus{
			ContainerId: id,
			State:       pb.ContainerState_CREATED,
			CreatedAt:   now,
			Config:      config,
			IoStats:     &pb.IOStats{},
		},
		stdoutBroadcast:  make(chan []byte, 100),
		stderrBroadcast:  make(chan []byte, 100),
		messageBroadcast: make(chan string, 100),
		exitCh:           make(chan int32, 1),
		ctx:              ctx,
		cancel:           cancel,
	}
}

func (c *Container) Start(isolationRunnerPath string) error {
	c.stateMu.Lock()
	if c.state.State != pb.ContainerState_CREATED {
		c.stateMu.Unlock()
		return fmt.Errorf("container already started")
	}
	c.stateMu.Unlock()

	cmd := exec.CommandContext(c.ctx, isolationRunnerPath)
	cmd.Env = append(cmd.Env, "BASTION_ADDRESS=localhost:50054")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	c.stdinWriter = stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	c.cmd = cmd

	c.stateMu.Lock()
	c.state.State = pb.ContainerState_RUNNING
	now := fmt.Sprintf("%d", time.Now().Unix())
	c.state.StartedAt = &now
	c.state.Pid = proto.Int32(int32(cmd.Process.Pid))
	c.stateMu.Unlock()

	config := c.buildConfig()
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if _, err := stdin.Write(configJSON); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	if _, err := stdin.Write([]byte("\n")); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	// SECURITY: Clear credentials from memory immediately after sending
	for i := range configJSON {
		configJSON[i] = 0
	}
	if configMap, ok := config["config"].(map[string]any); ok {
		if imageSpec, ok := configMap["image_spec"].(map[string]any); ok {
			if auth, ok := imageSpec["auth"].(map[string]any); ok {
				auth["password"] = ""
				auth["username"] = ""
			}
		}
	}

	go c.readOutput(stdout, true)
	go c.readOutput(stderr, false)
	go c.monitor()

	return nil
}

func (c *Container) buildConfig() map[string]any {
	hexID := c.ID
	if len(hexID) > 16 {
		hexID = hexID[:16]
	}

	networkRules := []map[string]any{}
	if c.Config.Network != nil {
		for _, rule := range c.Config.Network.Rules {
			if rule.Action == "allow" {
				dest := "0.0.0.0/0"
				if rule.Destination != nil {
					dest = *rule.Destination
				}
				ports := []string{}
				// Handle port range
				if rule.PortRangeStart != nil {
					if rule.PortRangeEnd != nil && *rule.PortRangeEnd > *rule.PortRangeStart {
						// Port range: start-end
						ports = append(ports, fmt.Sprintf("%d-%d", *rule.PortRangeStart, *rule.PortRangeEnd))
					} else {
						// Single port
						ports = append(ports, fmt.Sprintf("%d", *rule.PortRangeStart))
					}
				}
				networkRules = append(networkRules, map[string]any{
					"cidr":        dest,
					"description": "",
					"ports":       ports,
				})
			}
		}
	}

	defaultPolicy := "deny"
	allowDNS := false
	dnsServers := []string{}
	if c.Config.Network != nil && c.Config.Network.DefaultPolicy != nil {
		defaultPolicy = *c.Config.Network.DefaultPolicy
	}
	if c.Config.Network != nil && len(c.Config.Network.DnsServers) > 0 {
		allowDNS = true
		dnsServers = c.Config.Network.DnsServers
	}

	// Build container config, only include resource limits if they're set
	containerConfig := map[string]any{
		"runtime":         "runsc",
		"readonly_rootfs": false,
		"tmpfs":           []string{},
		"environment":     c.Config.Env,
		"working_dir":     c.Config.Workdir,
	}

	// Only include memory_limit if it's non-empty
	if memLimit := c.Config.Resources.GetMemoryLimit(); memLimit != "" {
		containerConfig["memory_limit"] = memLimit
	}

	// Only include cpu_limit if it's non-empty
	if cpuLimit := c.Config.Resources.GetCpuLimit(); cpuLimit != "" {
		containerConfig["cpu_limit"] = cpuLimit
	}

	return map[string]any{
		"type": "config",
		"config": map[string]any{
			"image_spec":     c.buildImageSpec(),
			"command":        c.Config.Command,
			"args":           c.Config.Args,
			"container_name": hexID,
			"bridge_name":    "bridge",
			"subnet":         nil,
			"config": map[string]any{
				"version": "1.0.0",
				"network": map[string]any{
					"default_policy":       defaultPolicy,
					"block_metadata":       true,
					"allow_dns":            allowDNS,
					"dns_servers":          dnsServers,
					"allowed_destinations": []string{},
					"whitelist":            networkRules,
					"blacklist":            []map[string]any{},
				},
				"container": containerConfig,
				"execution": map[string]any{
					"attach_stdin":    true,
					"attach_stdout":   true,
					"attach_stderr":   true,
					"tty":             false,
					"interactive":     true,
					"auto_cleanup":    c.Config.Cleanup,
					"timeout_seconds": c.Config.TimeoutSecs,
				},
				"logging": map[string]any{
					"enabled": true,
					"level":   "info",
				},
			},
		},
	}
}

// buildImageSpec converts ImageSpec proto to JSON map for isolation-runner
// SECURITY: Credentials included here will be cleared after serialization
func (c *Container) buildImageSpec() map[string]any {
	spec := c.Config.ImageSpec
	if spec == nil {
		return map[string]any{
			"registry": "registry-1.docker.io",
			"image":    "library/alpine:latest",
		}
	}

	imageSpec := map[string]any{
		"image": spec.Image,
	}

	registry := spec.GetRegistry()
	if registry == "" {
		registry = "registry-1.docker.io"
	}
	imageSpec["registry"] = registry

	if basicAuth := spec.GetBasicAuth(); basicAuth != nil {
		imageSpec["auth"] = map[string]any{
			"type":     "basic",
			"username": basicAuth.Username,
			"password": basicAuth.Password,
		}
	}

	return imageSpec
}

// getImageDisplayName returns sanitized image name for logging (no credentials)
func (c *Container) getImageDisplayName() string {
	spec := c.Config.ImageSpec
	if spec == nil {
		return "unknown"
	}

	registry := spec.GetRegistry()
	if registry == "" || registry == "registry-1.docker.io" {
		return spec.Image
	}

	return fmt.Sprintf("%s/%s", registry, spec.Image)
}

func (c *Container) readOutput(reader io.Reader, isStdout bool) {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		lineLen := len(line)

		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err == nil {
			c.handleJSONMessage(msg)
			continue
		}

		data := make([]byte, lineLen+1)
		copy(data, line)
		data[lineLen] = '\n'

		if isStdout {
			select {
			case c.stdoutBroadcast <- data:
			default:
			}
		} else {
			select {
			case c.stderrBroadcast <- data:
			default:
			}
		}
	}
}

func (c *Container) handleJSONMessage(msg map[string]any) {
	msgType, ok := msg["type"].(string)
	if !ok {
		return
	}

	switch msgType {
	case "container:stdout":
		if data, ok := msg["data"].(map[string]any); ok {
			if text, ok := data["data"].(string); ok {
				output := []byte(text)
				select {
				case c.stdoutBroadcast <- output:
				default:
				}
			}
		}

	case "container:stderr":
		if data, ok := msg["data"].(map[string]any); ok {
			if text, ok := data["data"].(string); ok {
				output := []byte(text)
				select {
				case c.stderrBroadcast <- output:
				default:
				}
			}
		}

	case "container:exit":
		if data, ok := msg["data"].(map[string]any); ok {
			if code, ok := data["code"].(float64); ok {
				select {
				case c.exitCh <- int32(code):
				default:
				}
			}
		}

	case "container_exit":
		// Handle container_exit from isolation-runner
		if code, ok := msg["exit_code"].(float64); ok {
			select {
			case c.exitCh <- int32(code):
			default:
			}
		}

	case "info", "debug", "warning", "error":
		msgBytes, _ := json.Marshal(msg)
		msgStr := string(msgBytes)
		select {
		case c.messageBroadcast <- msgStr:
		default:
		}

	// Handle structured lifecycle events
	case "container_created", "container_started", "image_pull_started",
		"image_pull_completed", "container_ip_ready", "network_isolation_ready",
		"container_terminating", "container_exited", "container_ready":
		msgBytes, _ := json.Marshal(msg)
		msgStr := string(msgBytes)
		select {
		case c.messageBroadcast <- msgStr:
		default:
		}
	}
}

func (c *Container) monitor() {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}

	err := c.cmd.Wait()
	exitCode := int32(0)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = int32(exitErr.ExitCode())
		} else {
			exitCode = 1
		}
	}

	// Brief sleep to allow readOutput goroutines to finish reading final data from pipes
	time.Sleep(50 * time.Millisecond)

	c.stateMu.Lock()
	nowUnix := time.Now().Unix()
	nowStr := fmt.Sprintf("%d", nowUnix)
	c.state.FinishedAt = &nowStr
	c.state.ExitCode = &exitCode
	cleanupAfter := nowUnix + 60
	c.state.CleanupAfter = &cleanupAfter

	if exitCode == 0 {
		c.state.State = pb.ContainerState_EXITED
	} else {
		c.state.State = pb.ContainerState_FAILED
	}
	c.stateMu.Unlock()

	select {
	case c.exitCh <- exitCode:
	default:
	}

	c.cancel()
}

func (c *Container) Terminate(force bool, timeoutSecs uint32) error {
	c.stateMu.Lock()
	state := c.state.State

	// If container never started or already exited, just mark as terminated
	if state == pb.ContainerState_CREATED || state == pb.ContainerState_EXITED ||
		state == pb.ContainerState_FAILED || state == pb.ContainerState_TERMINATED {
		c.state.State = pb.ContainerState_TERMINATED
		if c.state.FinishedAt == nil {
			finishedAt := fmt.Sprintf("%d", time.Now().Unix())
			c.state.FinishedAt = &finishedAt
		}
		c.stateMu.Unlock()
		return nil
	}
	c.stateMu.Unlock()

	if c.cmd == nil || c.cmd.Process == nil {
		return fmt.Errorf("no process to terminate")
	}

	if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	timeout := time.Duration(timeoutSecs) * time.Second
	if timeout == 0 {
		if force {
			timeout = 3 * time.Second
		} else {
			timeout = 10 * time.Second
		}
	}

	done := make(chan struct{})
	go func() {
		c.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.stateMu.Lock()
		c.state.State = pb.ContainerState_TERMINATED
		c.stateMu.Unlock()
		return nil

	case <-time.After(timeout):
		if err := c.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
		c.stateMu.Lock()
		c.state.State = pb.ContainerState_TERMINATED
		c.stateMu.Unlock()
		return nil
	}
}

func (c *Container) Wait(timeoutSecs uint32) (int32, error) {
	if timeoutSecs == 0 {
		exitCode := <-c.exitCh
		return exitCode, nil
	}

	select {
	case exitCode := <-c.exitCh:
		return exitCode, nil
	case <-time.After(time.Duration(timeoutSecs) * time.Second):
		return 0, fmt.Errorf("timeout waiting for container")
	}
}

func (c *Container) GetState() *pb.ContainerStatus {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// SECURITY: Clone config and clear auth credentials
	safeConfig := proto.Clone(c.state.Config).(*pb.ContainerConfig)
	if safeConfig.ImageSpec != nil {
		safeConfig.ImageSpec = &pb.ImageSpec{
			Registry: safeConfig.ImageSpec.Registry,
			Image:    safeConfig.ImageSpec.Image,
			// Auth intentionally omitted
		}
	}

	state := &pb.ContainerStatus{
		ContainerId:  c.state.ContainerId,
		State:        c.state.State,
		CreatedAt:    c.state.CreatedAt,
		StartedAt:    c.state.StartedAt,
		FinishedAt:   c.state.FinishedAt,
		ExitCode:     c.state.ExitCode,
		Pid:          c.state.Pid,
		Config:       safeConfig,
		IoStats:      c.state.IoStats,
		CleanupAfter: c.state.CleanupAfter,
	}
	return state
}

func (c *Container) WriteStdin(data []byte) error {
	if c.stdinWriter == nil {
		return fmt.Errorf("stdin not available")
	}

	// Encode stdin data as JSON message for isolation-runner
	// Format: {"type":"stdin","data":"<base64-encoded-data>"}
	msg := map[string]string{
		"type": "stdin",
		"data": base64.StdEncoding.EncodeToString(data),
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal stdin message: %w", err)
	}

	// Write JSON message followed by newline
	if _, err := c.stdinWriter.Write(jsonData); err != nil {
		return err
	}
	if _, err := c.stdinWriter.Write([]byte("\n")); err != nil {
		return err
	}

	return nil
}

func (c *Container) SubscribeStdout() <-chan []byte {
	return c.stdoutBroadcast
}

func (c *Container) SubscribeStderr() <-chan []byte {
	return c.stderrBroadcast
}

func (c *Container) SubscribeMessages() <-chan string {
	return c.messageBroadcast
}

func (c *Container) Close() {
	c.closeOnce.Do(func() {
		c.cancel()
		if c.stdinWriter != nil {
			c.stdinWriter.Close()
		}
		close(c.stdoutBroadcast)
		close(c.stderrBroadcast)
		close(c.messageBroadcast)
	})
}
