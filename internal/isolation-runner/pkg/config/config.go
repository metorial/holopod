package config

import (
	"fmt"
	"strings"
)

type Config struct {
	Version   string          `json:"version"`
	Network   NetworkConfig   `json:"network"`
	Container ContainerConfig `json:"container"`
	Execution ExecutionConfig `json:"execution"`
	Logging   LoggingConfig   `json:"logging"`
}

type NetworkConfig struct {
	Whitelist     []WhitelistEntry `json:"whitelist"`
	Blacklist     []BlacklistEntry `json:"blacklist"`
	DefaultPolicy string           `json:"default_policy"`
	BlockMetadata bool             `json:"block_metadata"`
	AllowDNS      bool             `json:"allow_dns"`
	DNSServers    []string         `json:"dns_servers"`
}

type WhitelistEntry struct {
	CIDR        string   `json:"cidr"`
	Description string   `json:"description"`
	Ports       []string `json:"ports"`
}

type BlacklistEntry struct {
	CIDR        string `json:"cidr"`
	Description string `json:"description"`
}

type ContainerConfig struct {
	Runtime        string            `json:"runtime"`
	MemoryLimit    *string           `json:"memory_limit"`
	CPULimit       *string           `json:"cpu_limit"`
	ReadonlyRootfs bool              `json:"readonly_rootfs"`
	Tmpfs          []string          `json:"tmpfs"`
	Environment    map[string]string `json:"environment"`
	WorkingDir     *string           `json:"working_dir"`
}

type ExecutionConfig struct {
	TimeoutSeconds *int64 `json:"timeout_seconds"`
	AutoCleanup    bool   `json:"auto_cleanup"`
	Interactive    bool   `json:"interactive"`
	AttachStdin    bool   `json:"attach_stdin"`
	AttachStdout   bool   `json:"attach_stdout"`
	AttachStderr   bool   `json:"attach_stderr"`
	TTY            bool   `json:"tty"`
}

type LoggingConfig struct {
	Enabled            bool    `json:"enabled"`
	LogNetworkAttempts bool    `json:"log_network_attempts"`
	LogFile            *string `json:"log_file"`
	LogLevel           string  `json:"log_level"`
}

func DefaultConfig() *Config {
	return &Config{
		Version:   "1.0",
		Network:   DefaultNetworkConfig(),
		Container: DefaultContainerConfig(),
		Execution: DefaultExecutionConfig(),
		Logging:   DefaultLoggingConfig(),
	}
}

func DefaultNetworkConfig() NetworkConfig {
	return NetworkConfig{
		Whitelist:     []WhitelistEntry{},
		Blacklist:     []BlacklistEntry{},
		DefaultPolicy: "deny",
		BlockMetadata: true,
		AllowDNS:      false,
		DNSServers:    []string{"8.8.8.8", "1.1.1.1"},
	}
}

func DefaultContainerConfig() ContainerConfig {
	return ContainerConfig{
		Runtime:        "runsc",
		MemoryLimit:    nil,
		CPULimit:       nil,
		ReadonlyRootfs: false,
		Tmpfs:          []string{},
		Environment:    make(map[string]string),
		WorkingDir:     nil,
	}
}

func DefaultExecutionConfig() ExecutionConfig {
	return ExecutionConfig{
		TimeoutSeconds: nil,
		AutoCleanup:    true,
		Interactive:    false,
		AttachStdin:    true,
		AttachStdout:   true,
		AttachStderr:   true,
		TTY:            false,
	}
}

func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		Enabled:            true,
		LogNetworkAttempts: true,
		LogFile:            nil,
		LogLevel:           "info",
	}
}

func ValidateImageReference(image string) error {
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("image name cannot be empty")
	}

	if len(image) > 512 {
		return fmt.Errorf("image name too long: %d bytes (max: 512)", len(image))
	}

	if strings.Contains(image, "..") || strings.Contains(image, "//") {
		return fmt.Errorf("suspicious pattern in image name: %s", image)
	}

	dangerousChars := []rune{';', '|', '&', '$', '`', '\n', '\r', '\\'}
	for _, c := range dangerousChars {
		if strings.ContainsRune(image, c) {
			return fmt.Errorf("dangerous character '%c' in image name", c)
		}
	}

	for _, c := range image {
		if c < 32 {
			return fmt.Errorf("image name contains control characters")
		}
	}

	return nil
}

// ValidateImageSpec validates registry, image, and credentials
func ValidateImageSpec(spec *ImageSpec) error {
	if spec == nil {
		return fmt.Errorf("image spec cannot be nil")
	}

	if err := ValidateImageReference(spec.Image); err != nil {
		return fmt.Errorf("invalid image: %w", err)
	}

	if spec.Registry != "" {
		if err := validateRegistry(spec.Registry); err != nil {
			return fmt.Errorf("invalid registry: %w", err)
		}
	}

	if spec.Auth != nil {
		if err := validateAuth(spec.Auth); err != nil {
			return fmt.Errorf("invalid auth: %w", err)
		}
	}

	return nil
}

func validateRegistry(registry string) error {
	if len(registry) > 253 {
		return fmt.Errorf("registry name too long")
	}

	// Check for injection attempts
	dangerousChars := []rune{';', '|', '&', '$', '`', '\n', '\r', '\\', ' '}
	for _, c := range dangerousChars {
		if strings.ContainsRune(registry, c) {
			return fmt.Errorf("registry contains invalid character: '%c'", c)
		}
	}

	if strings.Contains(registry, "..") {
		return fmt.Errorf("invalid registry hostname")
	}

	return nil
}

func validateAuth(auth *ImageAuth) error {
	if auth.Type != "basic" {
		return fmt.Errorf("unsupported auth type: %s", auth.Type)
	}

	if strings.TrimSpace(auth.Username) == "" {
		return fmt.Errorf("username cannot be empty")
	}

	if len(auth.Username) > 255 {
		return fmt.Errorf("username too long")
	}

	if len(auth.Password) > 1024 {
		return fmt.Errorf("password too long")
	}

	if strings.Contains(auth.Username, "\x00") || strings.Contains(auth.Password, "\x00") {
		return fmt.Errorf("credentials contain null bytes")
	}

	return nil
}

func ValidateEnvironmentVariables(env map[string]string) error {
	dangerousVars := []string{
		"LD_PRELOAD", "LD_LIBRARY_PATH", "PYTHONPATH",
		"PERL5LIB", "RUBYLIB", "NODE_PATH",
		"DYLD_INSERT_LIBRARIES", "DYLD_FORCE_FLAT_NAMESPACE",
	}

	for key, value := range env {
		for _, dv := range dangerousVars {
			if key == dv {
				return fmt.Errorf("environment variable '%s' is not allowed for security reasons", key)
			}
		}

		for _, c := range key {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
				return fmt.Errorf("invalid environment variable name: %s (only alphanumeric and underscore allowed)", key)
			}
		}

		if len(key) > 0 && key[0] >= '0' && key[0] <= '9' {
			return fmt.Errorf("environment variable name cannot start with number: %s", key)
		}

		if len(value) > 65536 {
			return fmt.Errorf("environment variable '%s' value too large: %d bytes (max: 64KB)", key, len(value))
		}

		if strings.Contains(value, "\x00") || strings.Contains(key, "\x00") {
			return fmt.Errorf("environment variable contains null byte")
		}
	}

	return nil
}
