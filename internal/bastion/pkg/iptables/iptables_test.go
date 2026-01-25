package iptables

import (
	"context"
	"net"
	"os"
	"os/exec"
	"testing"

	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
)

func TestCheckIPTables(t *testing.T) {
	ctx := context.Background()
	err := CheckIPTables(ctx)
	if err != nil {
		t.Skipf("iptables not available: %v", err)
	}
}

func TestApplyNetworkRule(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping test; requires root")
	}

	ctx := context.Background()
	chainName := "ISO-test1234567890ab"

	if err := runIPTables(ctx, "-N", chainName); err != nil {
		t.Fatalf("failed to create test chain: %v", err)
	}
	defer runIPTables(ctx, "-X", chainName)

	tests := []struct {
		name    string
		rule    *pb.NetworkRule
		action  string
		wantErr bool
	}{
		{
			name: "valid rule without ports",
			rule: &pb.NetworkRule{
				Cidr: "192.168.1.0/24",
			},
			action:  "ACCEPT",
			wantErr: false,
		},
		{
			name: "valid rule with ports",
			rule: &pb.NetworkRule{
				Cidr:  "10.0.0.0/8",
				Ports: []uint32{80, 443},
			},
			action:  "DROP",
			wantErr: false,
		},
		{
			name: "invalid CIDR",
			rule: &pb.NetworkRule{
				Cidr: "not-a-cidr",
			},
			action:  "ACCEPT",
			wantErr: true,
		},
		{
			name: "invalid port",
			rule: &pb.NetworkRule{
				Cidr:  "10.0.0.0/8",
				Ports: []uint32{0},
			},
			action:  "ACCEPT",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runIPTables(ctx, "-F", chainName)

			count, err := applyNetworkRule(ctx, chainName, tt.rule, tt.action)
			if (err != nil) != tt.wantErr {
				t.Errorf("applyNetworkRule() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && count == 0 {
				t.Error("applyNetworkRule() returned 0 rules applied")
			}
		})
	}
}

func TestSetupChain(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping test; requires root")
	}

	ctx := context.Background()
	chainName := "ISO-test2345678901ab"
	containerIP := net.ParseIP("172.17.0.2")

	if err := SetupChain(ctx, chainName, containerIP); err != nil {
		t.Fatalf("SetupChain() error = %v", err)
	}
	defer CleanupChain(ctx, chainName, containerIP.String())

	output, err := exec.CommandContext(ctx, "iptables", "-L", chainName, "-n").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to list chain: %v", err)
	}
	if len(output) == 0 {
		t.Error("chain appears to be empty")
	}
}

func TestApplyRules(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping test; requires root")
	}

	ctx := context.Background()
	chainName := "ISO-test3456789012ab"
	containerIP := net.ParseIP("172.17.0.3")

	if err := SetupChain(ctx, chainName, containerIP); err != nil {
		t.Fatalf("SetupChain() error = %v", err)
	}
	defer CleanupChain(ctx, chainName, containerIP.String())

	tests := []struct {
		name    string
		policy  *pb.NetworkPolicy
		wantErr bool
	}{
		{
			name: "deny with whitelist",
			policy: &pb.NetworkPolicy{
				Policy:        "deny",
				BlockMetadata: true,
				AllowDns:      true,
				Whitelist: []*pb.NetworkRule{
					{Cidr: "8.8.8.8/32", Ports: []uint32{443}},
				},
			},
			wantErr: false,
		},
		{
			name: "allow with blacklist",
			policy: &pb.NetworkPolicy{
				Policy:        "allow",
				BlockMetadata: true,
				Blacklist: []*pb.NetworkRule{
					{Cidr: "192.168.1.0/24"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid policy mode",
			policy: &pb.NetworkPolicy{
				Policy: "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runIPTables(ctx, "-F", chainName)

			count, err := ApplyRules(ctx, chainName, tt.policy)
			if (err != nil) != tt.wantErr {
				t.Errorf("ApplyRules() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && count == 0 {
				t.Error("ApplyRules() returned 0 rules applied")
			}
		})
	}
}

func TestCleanupChain(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping test; requires root")
	}

	ctx := context.Background()

	t.Run("cleanup with containerIP", func(t *testing.T) {
		chainName := "ISO-test4567890123ab"
		containerIP := net.ParseIP("172.17.0.4")

		if err := SetupChain(ctx, chainName, containerIP); err != nil {
			t.Fatalf("SetupChain() error = %v", err)
		}

		if err := CleanupChain(ctx, chainName, containerIP.String()); err != nil {
			t.Fatalf("CleanupChain() error = %v", err)
		}

		output, err := exec.CommandContext(ctx, "iptables", "-L", chainName, "-n").CombinedOutput()
		if err == nil {
			t.Errorf("chain still exists after cleanup: %s", output)
		}
	})

	t.Run("cleanup without containerIP", func(t *testing.T) {
		chainName := "ISO-test5678901234ab"
		containerIP := net.ParseIP("172.17.0.5")

		if err := SetupChain(ctx, chainName, containerIP); err != nil {
			t.Fatalf("SetupChain() error = %v", err)
		}

		if err := CleanupChain(ctx, chainName, ""); err != nil {
			t.Fatalf("CleanupChain() error = %v", err)
		}

		output, err := exec.CommandContext(ctx, "iptables", "-L", chainName, "-n").CombinedOutput()
		if err == nil {
			t.Errorf("chain still exists after cleanup: %s", output)
		}
	})
}
