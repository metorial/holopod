package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestValidateChainName(t *testing.T) {
	tests := []struct {
		name      string
		chainName string
		wantErr   bool
	}{
		{"valid chain name", "ISO-0123456789abcdef", false},
		{"wrong prefix", "FOO-0123456789abcdef", true},
		{"uppercase hex", "ISO-0123456789ABCDEF", true},
		{"too short", "ISO-0123456789abc", true},
		{"too long", "ISO-0123456789abcdef0", true},
		{"exceeds max length", "ISO-0123456789abcdef01234567890", true},
		{"special chars", "ISO-0123456789abcd$f", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChainName(tt.chainName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateChainName() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateContainerIP(t *testing.T) {
	tests := []struct {
		name    string
		ipStr   string
		wantErr bool
	}{
		{"valid 10.x", "10.0.0.1", false},
		{"valid 172.16-31.x", "172.16.0.1", false},
		{"valid 172.31.x", "172.31.255.254", false},
		{"valid 192.168.x", "192.168.1.1", false},
		{"public IP", "8.8.8.8", true},
		{"public IP 2", "1.1.1.1", true},
		{"not an IP", "not-an-ip", true},
		{"IPv6", "::1", true},
		{"172.15.x out of range", "172.15.0.1", true},
		{"172.32.x out of range", "172.32.0.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, err := ValidateContainerIP(tt.ipStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContainerIP() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && ip == nil {
				t.Error("ValidateContainerIP() returned nil IP for valid input")
			}
		})
	}
}

func TestValidateCIDR(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		wantErr bool
	}{
		{"valid /24", "192.168.0.0/24", false},
		{"valid /8", "10.0.0.0/8", false},
		{"valid /32", "192.168.1.1/32", false},
		{"not a CIDR", "not-a-cidr", true},
		{"invalid prefix", "192.168.0.0/33", true},
		{"missing prefix", "192.168.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipNet, err := ValidateCIDR(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCIDR() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && ipNet == nil {
				t.Error("ValidateCIDR() returned nil IPNet for valid input")
			}
		})
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    uint32
		wantErr bool
	}{
		{"port 80", 80, false},
		{"port 443", 443, false},
		{"port 65535", 65535, false},
		{"port 1", 1, false},
		{"port 0", 0, true},
		{"port 65536", 65536, true},
		{"port 100000", 100000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePort(tt.port)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePort() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDNSServer(t *testing.T) {
	tests := []struct {
		name    string
		dnsIP   string
		wantErr bool
	}{
		{"valid public DNS", "8.8.8.8", false},
		{"valid private DNS", "192.168.1.1", false},
		{"loopback", "127.0.0.1", true},
		{"AWS metadata", "169.254.169.254", true},
		{"Azure metadata", "168.63.129.16", true},
		{"Alibaba metadata", "100.100.100.200", true},
		{"link-local", "169.254.1.1", true},
		{"not an IP", "not-an-ip", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, err := ValidateDNSServer(tt.dnsIP)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDNSServer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && ip == nil {
				t.Error("ValidateDNSServer() returned nil IP for valid input")
			}
		})
	}
}

func TestValidatePolicyMode(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		wantErr bool
	}{
		{"allow", "allow", false},
		{"deny", "deny", false},
		{"invalid", "invalid", true},
		{"block", "block", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePolicyMode(tt.policy)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePolicyMode() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateNetworkName(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		wantErr     bool
	}{
		{"valid name", "iso-net-abc123", false},
		{"valid with hyphens", "iso-net-test-network-1", false},
		{"wrong prefix", "foo-pool-abc123", true},
		{"no prefix", "pool-abc123", true},
		{"path traversal", "iso-net-../etc/passwd", true},
		{"command injection", "iso-net-abc; rm -rf /", true},
		{"special chars", "iso-net-abc$123", true},
		{"uppercase", "iso-net-ABC123", true},
		{"too short", "iso-net-", true},
		{"too long", "iso-net-" + string(make([]byte, 60)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNetworkName(tt.networkName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNetworkName() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateConfigHash(t *testing.T) {
	subnet := "172.20.0.0/24"
	minIPs := uint32(254)
	driver := "bridge"

	hasher := sha256.New()
	hasher.Write([]byte(subnet))
	hasher.Write([]byte{byte(minIPs), byte(minIPs >> 8), byte(minIPs >> 16), byte(minIPs >> 24)})
	hasher.Write([]byte(driver))
	correctHash := hex.EncodeToString(hasher.Sum(nil))

	tests := []struct {
		name         string
		providedHash string
		subnetRange  *string
		minIPs       uint32
		driver       string
		wantErr      bool
	}{
		{"correct hash", correctHash, &subnet, minIPs, driver, false},
		{"wrong hash", "deadbeef1234567890abcdef", &subnet, minIPs, driver, true},
		{"nil subnet", correctHash, nil, minIPs, driver, true},
		{"different subnet", correctHash, strPtr("10.0.0.0/24"), minIPs, driver, true},
		{"different minIPs", correctHash, &subnet, 100, driver, true},
		{"different driver", correctHash, &subnet, minIPs, "overlay", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfigHash(tt.providedHash, tt.subnetRange, tt.minIPs, tt.driver)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfigHash() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateContainerID(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		wantErr     bool
	}{
		{"12 char short form", "abc123def456", false},
		{"64 char full", "abc123def456789012345678901234567890123456789012345678901234", false},
		{"empty", "", true},
		{"too short", "abc123", true},
		{"too long", string(make([]byte, 65)), true},
		{"uppercase", "ABC123DEF456", true},
		{"non-hex", "xyz123def456", false},             // Now allowed: lowercase alphanumeric
		{"with hyphens", "abc-123-def-456", false},     // Now allowed: hyphens permitted
		{"with underscores", "abc_123_def_456", false}, // Underscores also permitted
		{"with invalid chars", "abc@123!def", true},    // Special chars not allowed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContainerID(tt.containerID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContainerID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
