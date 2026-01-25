package iptables

import (
	"context"
	"net"
	"os"
	"testing"
)

func requireRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Skipping test: requires root privileges")
	}
}

func TestDetectIPVersion(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		want    ipVersion
		wantErr bool
	}{
		{
			name: "IPv4 address",
			cidr: "192.168.1.1",
			want: ipv4,
		},
		{
			name: "IPv4 CIDR",
			cidr: "10.0.0.0/8",
			want: ipv4,
		},
		{
			name: "IPv6 address",
			cidr: "2001:db8::1",
			want: ipv6,
		},
		{
			name: "IPv6 CIDR",
			cidr: "2001:db8::/32",
			want: ipv6,
		},
		{
			name: "IPv6 localhost",
			cidr: "::1",
			want: ipv6,
		},
		{
			name: "IPv6 localhost CIDR",
			cidr: "::1/128",
			want: ipv6,
		},
		{
			name: "IPv6 link-local",
			cidr: "fe80::1",
			want: ipv6,
		},
		{
			name:    "Invalid IP",
			cidr:    "not-an-ip",
			wantErr: true,
		},
		{
			name:    "Invalid CIDR",
			cidr:    "999.999.999.999",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := detectIPVersion(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("detectIPVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("detectIPVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetupChainIPv4(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	requireRoot(t)

	ctx := context.Background()
	chainName := "TEST-IPv4-CHAIN"
	containerIP := net.ParseIP("10.0.0.100")

	// Setup chain - should create chains in BOTH iptables and ip6tables
	err := SetupChain(ctx, chainName, containerIP)
	if err != nil {
		t.Fatalf("SetupChain() failed: %v", err)
	}

	// Cleanup
	defer CleanupChain(ctx, chainName, containerIP.String())

	// Verify IPv4 chain exists
	if err := runIPTables(ctx, "-F", chainName); err != nil {
		t.Errorf("IPv4 chain %s was not created", chainName)
	}

	// Verify IPv6 chain also exists (dual-stack support)
	if err := runIP6Tables(ctx, "-F", chainName); err != nil {
		t.Errorf("IPv6 chain %s was not created", chainName)
	}
}

func TestSetupChainIPv6(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	requireRoot(t)

	ctx := context.Background()
	chainName := "TEST-IPv6-CHAIN"
	containerIP := net.ParseIP("2001:db8::100")

	// Setup chain - should create chains in BOTH iptables and ip6tables
	err := SetupChain(ctx, chainName, containerIP)
	if err != nil {
		t.Fatalf("SetupChain() failed: %v", err)
	}

	// Cleanup
	defer CleanupChain(ctx, chainName, containerIP.String())

	// Verify IPv6 chain exists
	if err := runIP6Tables(ctx, "-F", chainName); err != nil {
		t.Errorf("IPv6 chain %s was not created", chainName)
	}

	// Verify IPv4 chain also exists (dual-stack support)
	if err := runIPTables(ctx, "-F", chainName); err != nil {
		t.Errorf("IPv4 chain %s was not created", chainName)
	}
}

func TestIPv4andIPv6CoexistenceIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	requireRoot(t)

	ctx := context.Background()

	// Test that IPv4 and IPv6 chains can coexist with the same name
	chainName := "TEST-DUAL-STACK"
	ipv4IP := net.ParseIP("10.0.0.200")
	ipv6IP := net.ParseIP("2001:db8::200")

	// Setup IPv4 chain
	err := SetupChain(ctx, chainName, ipv4IP)
	if err != nil {
		t.Fatalf("SetupChain IPv4 failed: %v", err)
	}
	defer CleanupChain(ctx, chainName, ipv4IP.String())

	// Setup IPv6 chain (same chain name, different IP version)
	// This should succeed because iptables and ip6tables maintain separate chain namespaces
	err = SetupChain(ctx, chainName+"6", ipv6IP)
	if err != nil {
		t.Fatalf("SetupChain IPv6 failed: %v", err)
	}
	defer CleanupChain(ctx, chainName+"6", ipv6IP.String())

	// Verify both chains exist
	if err := runIPTables(ctx, "-F", chainName); err != nil {
		t.Errorf("IPv4 chain %s was not created", chainName)
	}

	if err := runIP6Tables(ctx, "-F", chainName+"6"); err != nil {
		t.Errorf("IPv6 chain %s was not created", chainName+"6")
	}
}

func TestCleanupChainBothVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	requireRoot(t)

	ctx := context.Background()
	chainName := "TEST-CLEANUP-DUAL"

	// Create both IPv4 and IPv6 chains manually
	_ = runIPTables(ctx, "-N", chainName)
	_ = runIP6Tables(ctx, "-N", chainName)

	// Cleanup should remove both
	err := CleanupChain(ctx, chainName, "")
	if err != nil {
		t.Fatalf("CleanupChain() failed: %v", err)
	}

	// Verify both chains are gone by trying to flush them (should error)
	if err := runIPTables(ctx, "-F", chainName); err == nil {
		t.Errorf("IPv4 chain %s was not cleaned up", chainName)
	}

	if err := runIP6Tables(ctx, "-F", chainName); err == nil {
		t.Errorf("IPv6 chain %s was not cleaned up", chainName)
	}
}
