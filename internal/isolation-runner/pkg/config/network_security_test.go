package config

import (
	"net"
	"strings"
	"testing"
)

func TestValidateWhitelistEntry_LocalhostBlocked(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "Localhost IPv4 exact",
			cidr:    "127.0.0.1/32",
			wantErr: true,
			errMsg:  "localhost",
		},
		{
			name:    "Localhost IPv4 range",
			cidr:    "127.0.0.0/8",
			wantErr: true,
			errMsg:  "localhost",
		},
		{
			name:    "Localhost IPv6",
			cidr:    "::1/128",
			wantErr: true,
			errMsg:  "localhost",
		},
		{
			name:    "AWS metadata service",
			cidr:    "169.254.169.254/32",
			wantErr: true,
			errMsg:  "metadata",
		},
		{
			name:    "Link-local range",
			cidr:    "169.254.0.0/16",
			wantErr: true,
			errMsg:  "forbidden",
		},
		{
			name:    "Valid public IP",
			cidr:    "8.8.8.8/32",
			wantErr: false,
		},
		{
			name:    "Valid public range",
			cidr:    "1.1.1.0/24",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := WhitelistEntry{
				CIDR:        tt.cidr,
				Description: "test",
			}

			err := ValidateWhitelistEntry(&entry)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error for CIDR %s, got none", tt.cidr)
				} else if tt.errMsg != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errMsg)) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for CIDR %s: %v", tt.cidr, err)
				}
			}
		})
	}
}

func TestValidateWhitelistEntry_InvalidFormats(t *testing.T) {
	tests := []struct {
		name  string
		entry WhitelistEntry
	}{
		{
			name: "Empty CIDR",
			entry: WhitelistEntry{
				CIDR: "",
			},
		},
		{
			name: "Invalid CIDR format",
			entry: WhitelistEntry{
				CIDR: "not-a-cidr",
			},
		},
		{
			name: "Invalid port",
			entry: WhitelistEntry{
				CIDR:  "8.8.8.8/32",
				Ports: []string{"invalid"},
			},
		},
		{
			name: "Port out of range",
			entry: WhitelistEntry{
				CIDR:  "8.8.8.8/32",
				Ports: []string{"70000"},
			},
		},
		{
			name: "Port zero",
			entry: WhitelistEntry{
				CIDR:  "8.8.8.8/32",
				Ports: []string{"0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWhitelistEntry(&tt.entry)
			if err == nil {
				t.Errorf("Expected error for test %s, got none", tt.name)
			}
		})
	}
}

func TestEnforceSecurityRules_MandatoryBlocks(t *testing.T) {
	cfg := &NetworkConfig{
		Whitelist:     []WhitelistEntry{},
		Blacklist:     []BlacklistEntry{},
		DefaultPolicy: "deny",
		BlockMetadata: false, // Should be forced to true
		AllowDNS:      false,
		DNSServers:    []string{"8.8.8.8"},
	}

	err := EnforceSecurityRules(cfg)
	if err != nil {
		t.Fatalf("EnforceSecurityRules failed: %v", err)
	}

	// Check that BlockMetadata is forced to true
	if !cfg.BlockMetadata {
		t.Error("BlockMetadata should be forced to true")
	}

	// Check that mandatory blocks are in blacklist
	mustHave := []string{
		"127.0.0.0/8",        // Localhost
		"169.254.169.254/32", // Metadata
		"169.254.0.0/16",     // Link-local
	}

	for _, cidr := range mustHave {
		found := false
		for _, entry := range cfg.Blacklist {
			if entry.CIDR == cidr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Mandatory block %s not found in blacklist", cidr)
		}
	}
}

func TestEnforceSecurityRules_PrivateRangesBlocked(t *testing.T) {
	cfg := &NetworkConfig{
		Whitelist:     []WhitelistEntry{},
		Blacklist:     []BlacklistEntry{},
		DefaultPolicy: "deny",
		BlockMetadata: true,
		AllowDNS:      false,
		DNSServers:    []string{"8.8.8.8"},
	}

	err := EnforceSecurityRules(cfg)
	if err != nil {
		t.Fatalf("EnforceSecurityRules failed: %v", err)
	}

	// Check that private ranges are blocked when not whitelisted
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}

	for _, cidr := range privateRanges {
		found := false
		for _, entry := range cfg.Blacklist {
			if entry.CIDR == cidr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Private range %s not found in blacklist", cidr)
		}
	}
}

func TestEnforceSecurityRules_PrivateRangesWhitelisted(t *testing.T) {
	cfg := &NetworkConfig{
		Whitelist: []WhitelistEntry{
			{
				CIDR:        "10.0.1.0/24",
				Description: "Internal API",
			},
		},
		Blacklist:     []BlacklistEntry{},
		DefaultPolicy: "deny",
		BlockMetadata: true,
		AllowDNS:      false,
		DNSServers:    []string{"8.8.8.8"},
	}

	err := EnforceSecurityRules(cfg)
	if err != nil {
		t.Fatalf("EnforceSecurityRules failed: %v", err)
	}

	// The broader 10.0.0.0/8 should NOT be in blacklist since 10.0.1.0/24 is whitelisted
	for _, entry := range cfg.Blacklist {
		if entry.CIDR == "10.0.0.0/8" {
			t.Error("10.0.0.0/8 should not be in blacklist when 10.0.1.0/24 is whitelisted")
		}
	}

	// But other private ranges should still be blocked
	found172 := false
	found192 := false
	for _, entry := range cfg.Blacklist {
		if entry.CIDR == "172.16.0.0/12" {
			found172 = true
		}
		if entry.CIDR == "192.168.0.0/16" {
			found192 = true
		}
	}

	if !found172 {
		t.Error("172.16.0.0/12 should still be blocked")
	}
	if !found192 {
		t.Error("192.168.0.0/16 should still be blocked")
	}
}

func TestEnforceSecurityRules_CannotWhitelistLocalhost(t *testing.T) {
	tests := []struct {
		name string
		cidr string
	}{
		{"Localhost IPv4", "127.0.0.1/32"},
		{"Localhost range", "127.0.0.0/8"},
		{"Localhost IPv6", "::1/128"},
		{"Metadata service", "169.254.169.254/32"},
		{"Link-local", "169.254.0.0/16"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkConfig{
				Whitelist: []WhitelistEntry{
					{
						CIDR:        tt.cidr,
						Description: "Attempt to whitelist forbidden range",
					},
				},
				Blacklist:     []BlacklistEntry{},
				DefaultPolicy: "deny",
				BlockMetadata: true,
				AllowDNS:      false,
				DNSServers:    []string{"8.8.8.8"},
			}

			err := EnforceSecurityRules(cfg)
			if err == nil {
				t.Errorf("Expected error when trying to whitelist %s, got none", tt.cidr)
			}
		})
	}
}

func TestValidateNetworkConfig_InvalidDNS(t *testing.T) {
	tests := []struct {
		name       string
		dnsServers []string
		wantErr    bool
	}{
		{
			name:       "Valid public DNS",
			dnsServers: []string{"8.8.8.8", "1.1.1.1"},
			wantErr:    false,
		},
		{
			name:       "Invalid IP format",
			dnsServers: []string{"not-an-ip"},
			wantErr:    true,
		},
		{
			name:       "Localhost DNS blocked",
			dnsServers: []string{"127.0.0.1"},
			wantErr:    true,
		},
		{
			name:       "Metadata service as DNS blocked",
			dnsServers: []string{"169.254.169.254"},
			wantErr:    true,
		},
		{
			name:       "Link-local DNS blocked",
			dnsServers: []string{"169.254.1.1"},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkConfig{
				Whitelist:     []WhitelistEntry{},
				Blacklist:     []BlacklistEntry{},
				DefaultPolicy: "deny",
				BlockMetadata: true,
				AllowDNS:      true,
				DNSServers:    tt.dnsServers,
			}

			err := ValidateNetworkConfig(cfg)

			if tt.wantErr && err == nil {
				t.Errorf("Expected error for DNS servers %v, got none", tt.dnsServers)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error for DNS servers %v: %v", tt.dnsServers, err)
			}
		})
	}
}

func TestValidateNetworkConfig_DefaultPolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		wantErr bool
	}{
		{"Allow policy", "allow", false},
		{"Deny policy", "deny", false},
		{"Invalid policy", "invalid", true},
		{"Empty policy", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkConfig{
				Whitelist:     []WhitelistEntry{},
				Blacklist:     []BlacklistEntry{},
				DefaultPolicy: tt.policy,
				BlockMetadata: true,
				AllowDNS:      false,
				DNSServers:    []string{"8.8.8.8"},
			}

			err := ValidateNetworkConfig(cfg)

			if tt.wantErr && err == nil {
				t.Errorf("Expected error for policy '%s', got none", tt.policy)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error for policy '%s': %v", tt.policy, err)
			}
		})
	}
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		isPublic bool
	}{
		{"Google DNS", "8.8.8.8", true},
		{"Cloudflare DNS", "1.1.1.1", true},
		{"Localhost", "127.0.0.1", false},
		{"Private 10", "10.0.0.1", false},
		{"Private 172", "172.16.0.1", false},
		{"Private 192", "192.168.1.1", false},
		{"Metadata", "169.254.169.254", false},
		{"Link-local", "169.254.1.1", false},
		{"Multicast", "224.0.0.1", false},
		{"Broadcast", "255.255.255.255", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("Invalid IP address: %s", tt.ip)
			}

			result := IsPublicIP(ip)
			if result != tt.isPublic {
				t.Errorf("IsPublicIP(%s) = %v, want %v", tt.ip, result, tt.isPublic)
			}
		})
	}
}

func TestNetworksOverlap(t *testing.T) {
	tests := []struct {
		name    string
		net1    string
		net2    string
		overlap bool
	}{
		{
			name:    "Identical networks",
			net1:    "10.0.0.0/24",
			net2:    "10.0.0.0/24",
			overlap: true,
		},
		{
			name:    "net2 subset of net1",
			net1:    "10.0.0.0/8",
			net2:    "10.0.1.0/24",
			overlap: true,
		},
		{
			name:    "net1 subset of net2",
			net1:    "10.0.1.0/24",
			net2:    "10.0.0.0/8",
			overlap: true,
		},
		{
			name:    "No overlap",
			net1:    "10.0.0.0/24",
			net2:    "192.168.0.0/24",
			overlap: false,
		},
		{
			name:    "Localhost vs private",
			net1:    "127.0.0.0/8",
			net2:    "10.0.0.0/8",
			overlap: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, n1, err := net.ParseCIDR(tt.net1)
			if err != nil {
				t.Fatalf("Failed to parse net1 %s: %v", tt.net1, err)
			}

			_, n2, err := net.ParseCIDR(tt.net2)
			if err != nil {
				t.Fatalf("Failed to parse net2 %s: %v", tt.net2, err)
			}

			result := networksOverlap(n1, n2)
			if result != tt.overlap {
				t.Errorf("networksOverlap(%s, %s) = %v, want %v", tt.net1, tt.net2, result, tt.overlap)
			}
		})
	}
}

func TestMandatoryBlocksCannotBeRemoved(t *testing.T) {
	// Start with a config that tries to allow everything
	cfg := &NetworkConfig{
		Whitelist:     []WhitelistEntry{},
		Blacklist:     []BlacklistEntry{}, // Empty blacklist
		DefaultPolicy: "allow",            // Permissive policy
		BlockMetadata: false,              // Try to disable metadata blocking
		AllowDNS:      true,
		DNSServers:    []string{"8.8.8.8"},
	}

	// Enforce security rules
	err := EnforceSecurityRules(cfg)
	if err != nil {
		t.Fatalf("EnforceSecurityRules failed: %v", err)
	}

	// Verify that mandatory blocks are present despite the permissive config
	if !cfg.BlockMetadata {
		t.Error("BlockMetadata should be forced to true")
	}

	// Check all mandatory blocks are present
	mandatoryCount := 0
	for _, entry := range cfg.Blacklist {
		for _, mandatory := range MandatoryBlockedRanges {
			if entry.CIDR == mandatory {
				mandatoryCount++
				break
			}
		}
	}

	if mandatoryCount < len(MandatoryBlockedRanges) {
		t.Errorf("Only %d/%d mandatory blocks found in blacklist", mandatoryCount, len(MandatoryBlockedRanges))
	}
}
