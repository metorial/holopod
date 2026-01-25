package config

import (
	"testing"
)

func TestValidateImageReference(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{"valid alpine", "alpine:3.18", false},
		{"valid ubuntu", "ubuntu:latest", false},
		{"valid with registry", "myregistry.com/myimage:v1.0", false},
		{"valid with digest", "image@sha256:abc123def456", false},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"dangerous semicolon", "alpine; whoami", true},
		{"dangerous ampersand", "alpine && ls", true},
		{"dangerous pipe", "alpine|cat", true},
		{"dangerous dollar", "alpine$HOME", true},
		{"dangerous backtick", "alpine`whoami`", true},
		{"path traversal", "../../../etc/passwd", true},
		{"double slash", "image//path", true},
		{"too long", string(make([]byte, 600)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImageReference(tt.image)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImageReference() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateEnvironmentVariables(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			"valid vars",
			map[string]string{"PATH": "/usr/bin", "HOME": "/home/user", "MY_VAR_123": "value"},
			false,
		},
		{
			"LD_PRELOAD",
			map[string]string{"LD_PRELOAD": "/bad/lib.so"},
			true,
		},
		{
			"LD_LIBRARY_PATH",
			map[string]string{"LD_LIBRARY_PATH": "/bad/path"},
			true,
		},
		{
			"PYTHONPATH",
			map[string]string{"PYTHONPATH": "/bad/path"},
			true,
		},
		{
			"starts with number",
			map[string]string{"123INVALID": "value"},
			true,
		},
		{
			"contains hyphen",
			map[string]string{"INVALID-VAR": "value"},
			true,
		},
		{
			"contains space",
			map[string]string{"INVALID VAR": "value"},
			true,
		},
		{
			"null byte in key",
			map[string]string{"VAR\x00": "value"},
			true,
		},
		{
			"null byte in value",
			map[string]string{"VAR": "value\x00"},
			true,
		},
		{
			"value too large",
			map[string]string{"VAR": string(make([]byte, 70000))},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEnvironmentVariables(tt.env)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEnvironmentVariables() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Network.DefaultPolicy != "deny" {
		t.Errorf("expected default policy 'deny', got %s", cfg.Network.DefaultPolicy)
	}

	if !cfg.Network.BlockMetadata {
		t.Error("expected BlockMetadata to be true")
	}

	if !cfg.Execution.AutoCleanup {
		t.Error("expected AutoCleanup to be true")
	}

	if !cfg.Logging.Enabled {
		t.Error("expected Logging.Enabled to be true")
	}
}
