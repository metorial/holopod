package jsonmsg

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type OutputMessage struct {
	Type      string  `json:"type"`
	Message   *string `json:"message,omitempty"`
	ExitCode  *int    `json:"exit_code,omitempty"`
	Container *string `json:"container,omitempty"`
	Timestamp string  `json:"timestamp"`
}

// StructuredEvent is a flexible event structure for lifecycle events
type StructuredEvent struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

func Info(message string) {
	Emit(OutputMessage{
		Type:      "info",
		Message:   &message,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func Warning(message string) {
	Emit(OutputMessage{
		Type:      "warning",
		Message:   &message,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func Error(message string) {
	Emit(OutputMessage{
		Type:      "error",
		Message:   &message,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func ContainerExit(exitCode int) {
	Emit(OutputMessage{
		Type:      "container_exited",
		ExitCode:  &exitCode,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func ContainerName(name string) {
	Emit(OutputMessage{
		Type:      "container_name",
		Container: &name,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func ContainerStdout(data string) {
	msg := map[string]any{
		"type":      "container:stdout",
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"data": map[string]any{
			"data": data,
		},
	}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal output message: %v\n", err)
		return
	}
	fmt.Println(string(jsonData))
	os.Stdout.Sync() // Flush immediately
}

func ContainerStderr(data string) {
	msg := map[string]any{
		"type":      "container:stderr",
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"data": map[string]any{
			"data": data,
		},
	}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal output message: %v\n", err)
		return
	}
	fmt.Println(string(jsonData))
	os.Stdout.Sync() // Flush immediately
}

func Emit(msg OutputMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal output message: %v\n", err)
		return
	}
	fmt.Println(string(data))
	os.Stdout.Sync() // Flush immediately
}

// EmitEvent emits a structured event
func EmitEvent(event StructuredEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal event: %v\n", err)
		return
	}
	fmt.Println(string(data))
	os.Stdout.Sync() // Flush immediately
}

// Lifecycle Events - structured JSON output for important events

// ContainerCreated emits when a container has been created
func ContainerCreated(containerID string, containerName string, image string) {
	EmitEvent(StructuredEvent{
		Type:      "container_created",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"container_id":   containerID,
			"container_name": containerName,
			"image":          image,
		},
	})
}

// ContainerStarted emits when a container has started
func ContainerStarted(containerID string, containerName string, pid int) {
	EmitEvent(StructuredEvent{
		Type:      "container_started",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"container_id":   containerID,
			"container_name": containerName,
			"pid":            pid,
		},
	})
}

// ImagePullStarted emits when an image pull begins
func ImagePullStarted(image string, registry string, authenticated bool) {
	EmitEvent(StructuredEvent{
		Type:      "image_pull_started",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"image":         image,
			"registry":      registry,
			"authenticated": authenticated,
		},
	})
}

// ImagePullCompleted emits when an image pull completes successfully
func ImagePullCompleted(image string, registry string, alreadyPresent bool) {
	EmitEvent(StructuredEvent{
		Type:      "image_pull_completed",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"image":           image,
			"registry":        registry,
			"already_present": alreadyPresent,
		},
	})
}

// ContainerIPReady emits when container IP address is assigned
func ContainerIPReady(containerID string, ipAddress string, networkName string) {
	EmitEvent(StructuredEvent{
		Type:      "container_ip_ready",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"container_id": containerID,
			"ip_address":   ipAddress,
			"network":      networkName,
		},
	})
}

// NetworkIsolationReady emits when network isolation is configured
func NetworkIsolationReady(containerID string, chainName string, defaultPolicy string) {
	EmitEvent(StructuredEvent{
		Type:      "network_isolation_ready",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"container_id":   containerID,
			"chain_name":     chainName,
			"default_policy": defaultPolicy,
		},
	})
}

// ContainerTerminating emits when a container is being terminated
func ContainerTerminating(containerID string, reason string, force bool) {
	EmitEvent(StructuredEvent{
		Type:      "container_terminating",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"container_id": containerID,
			"reason":       reason,
			"force":        force,
		},
	})
}

// ContainerExited emits when a container exits (enhanced version with more details)
func ContainerExitedWithDetails(containerID string, exitCode int, duration string) {
	EmitEvent(StructuredEvent{
		Type:      "container_exited",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"container_id": containerID,
			"exit_code":    exitCode,
			"duration":     duration,
		},
	})
}

// ContainerReady emits when container is fully ready (started + network configured)
func ContainerReady(containerID string, ipAddress string) {
	EmitEvent(StructuredEvent{
		Type:      "container_ready",
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Data: map[string]any{
			"container_id": containerID,
			"ip_address":   ipAddress,
		},
	})
}
