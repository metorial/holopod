package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/uuid"
)

type ContainerInput struct {
	ImageSpec     *ImageSpec `json:"image_spec"`
	Command       []string   `json:"command"`
	Args          []string   `json:"args"`
	ContainerName *string    `json:"container_name"`
	BridgeName    *string    `json:"bridge_name"`
	Subnet        *string    `json:"subnet"`
	Config        Config     `json:"config"`
}

// ImageSpec matches the protobuf ImageSpec structure
type ImageSpec struct {
	Registry string     `json:"registry"`
	Image    string     `json:"image"`
	Auth     *ImageAuth `json:"auth,omitempty"`
}

// ImageAuth contains authentication credentials
type ImageAuth struct {
	Type     string `json:"type"` // "basic"
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func (c *ContainerInput) GetContainerName() string {
	// if c.ContainerName != nil {
	// 	return *c.ContainerName
	// }
	return fmt.Sprintf("hpod-%s", uuid.New().String()[:8])
}

func (c *ContainerInput) GetBridgeName() string {
	if c.BridgeName != nil {
		return *c.BridgeName
	}
	return fmt.Sprintf("iso-br-%s", uuid.New().String()[:8])
}

func (c *ContainerInput) GetContainerCommand() []string {
	return c.Command
}

func (c *ContainerInput) GetContainerArgs() []string {
	return c.Args
}

// HasCommand returns true if a custom command (entrypoint) is specified
func (c *ContainerInput) HasCommand() bool {
	return len(c.Command) > 0
}

// HasArgs returns true if custom args are specified
func (c *ContainerInput) HasArgs() bool {
	return len(c.Args) > 0
}

// GetFullImageReference returns the complete image reference for Docker API
func (c *ContainerInput) GetFullImageReference() string {
	if c.ImageSpec == nil {
		return "library/alpine:latest"
	}

	registry := c.ImageSpec.Registry
	if registry == "" || registry == "registry-1.docker.io" {
		return c.ImageSpec.Image
	}

	return fmt.Sprintf("%s/%s", registry, c.ImageSpec.Image)
}

// GetImageDisplayName returns sanitized image name for logging (no credentials)
func (c *ContainerInput) GetImageDisplayName() string {
	if c.ImageSpec == nil {
		return "unknown"
	}

	registry := c.ImageSpec.Registry
	if registry == "" || registry == "registry-1.docker.io" {
		return c.ImageSpec.Image
	}

	return fmt.Sprintf("%s/%s", registry, c.ImageSpec.Image)
}

func ReadInputFromStdin() (*ContainerInput, error) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read from stdin: %w", err)
	}

	if len(line) == 0 {
		return nil, fmt.Errorf("no input provided on stdin")
	}

	var msg struct {
		Type   string          `json:"type"`
		Config *ContainerInput `json:"config"`
	}

	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil, fmt.Errorf("failed to parse input JSON: %w", err)
	}

	if msg.Type != "config" {
		return nil, fmt.Errorf("expected config message, got: %s", msg.Type)
	}

	if msg.Config == nil {
		return nil, fmt.Errorf("config field is missing")
	}

	return msg.Config, nil
}
