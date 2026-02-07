package iptables

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/metorial/fleet/holopod/internal/bastion/pkg/validation"
	pb "github.com/metorial/fleet/holopod/internal/bastion/proto"
)

const defaultTimeout = 30 * time.Second

// ipVersion represents the IP version (IPv4 or IPv6) for iptables rules
type ipVersion int

const (
	ipv4 ipVersion = 4
	ipv6 ipVersion = 6
)

// CheckIPTables verifies that both iptables (IPv4) and ip6tables (IPv6) are available
func CheckIPTables(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Check iptables (IPv4)
	cmd := exec.CommandContext(ctx, "iptables", "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables not available: %w", err)
	}

	if !bytes.Contains(output, []byte("iptables")) {
		return fmt.Errorf("unexpected iptables version output: %s", output)
	}

	// Check ip6tables (IPv6)
	cmd6 := exec.CommandContext(ctx, "ip6tables", "--version")
	output6, err6 := cmd6.CombinedOutput()
	if err6 != nil {
		return fmt.Errorf("ip6tables not available: %w", err6)
	}

	if !bytes.Contains(output6, []byte("ip6tables")) {
		return fmt.Errorf("unexpected ip6tables version output: %s", output6)
	}

	return nil
}

// SetupChain creates iptables chains for both IPv4 and IPv6 traffic filtering.
// It creates chains in both iptables and ip6tables to support dual-stack rules,
// but only creates a FORWARD rule for the container's actual IP version.
func SetupChain(ctx context.Context, chainName string, containerIP net.IP) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Determine container's IP version
	containerVersion := ipv4
	if containerIP.To4() == nil {
		containerVersion = ipv6
	}

	// Create chains in BOTH iptables (IPv4) and ip6tables (IPv6)
	// This allows us to apply both IPv4 and IPv6 rules regardless of container IP version
	if err := runIPTables(ctx, "-N", chainName); err != nil {
		return err
	}

	if err := runIP6Tables(ctx, "-N", chainName); err != nil {
		// Cleanup IPv4 chain if IPv6 chain creation fails
		_ = runIPTables(ctx, "-X", chainName)
		return err
	}

	// Insert FORWARD rule only for the container's actual IP version
	// This ensures container traffic is directed to our chain for filtering
	if err := runIPTablesForVersion(ctx, containerVersion, "-I", "FORWARD", "1", "-s", containerIP.String(), "-j", chainName); err != nil {
		// Cleanup both chains if FORWARD rule fails
		_ = runIPTablesForVersion(ctx, containerVersion, "-X", chainName)
		_ = runIPTables(ctx, "-X", chainName)
		_ = runIP6Tables(ctx, "-X", chainName)
		return err
	}

	return nil
}

// ApplyRules applies network policy rules to an iptables chain.
// It handles both IPv4 (iptables) and IPv6 (ip6tables) rules appropriately.
func ApplyRules(ctx context.Context, chainName string, policy *pb.NetworkPolicy) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	if err := validation.ValidatePolicyMode(policy.Policy); err != nil {
		return 0, err
	}

	rulesApplied := 0

	// Always block cross-container communication on the default Docker bridge subnet(s).
	// This enforces isolation even when user policy would otherwise allow it.
	for _, subnet := range dockerBridgeSubnets(ctx) {
		version, err := detectIPVersion(subnet)
		if err != nil {
			continue
		}
		if err := runIPTablesForVersion(ctx, version, "-A", chainName, "-d", subnet, "-j", "DROP"); err != nil {
			return rulesApplied, err
		}
		rulesApplied++
	}

	// Apply metadata and security blocking rules for IPv4
	if policy.BlockMetadata {
		ipv4Rules := [][]string{}
		// Allow Docker embedded DNS (127.0.0.11) when DNS is enabled.
		if policy.AllowDns {
			for _, proto := range []string{"udp", "tcp"} {
				ipv4Rules = append(ipv4Rules, []string{"-A", chainName, "-d", "127.0.0.11/32", "-p", proto, "--dport", "53", "-j", "ACCEPT"})
			}
		}
		ipv4Rules = append(ipv4Rules, [][]string{
			{"-A", chainName, "-d", "169.254.169.254", "-j", "DROP"},         // AWS/GCP/Azure metadata
			{"-A", chainName, "-d", "168.63.129.16", "-j", "DROP"},           // Azure metadata
			{"-A", chainName, "-d", "100.100.100.200", "-j", "DROP"},         // Alibaba metadata
			{"-A", chainName, "-d", "169.254.0.0/16", "-j", "DROP"},          // Link-local
			{"-A", chainName, "-d", "127.0.0.0/8", "-j", "DROP"},             // Localhost
			{"-A", chainName, "-p", "udp", "--dport", "67:68", "-j", "DROP"}, // DHCP
		}...)
		for _, rule := range ipv4Rules {
			if err := runIPTables(ctx, rule...); err != nil {
				return rulesApplied, err
			}
			rulesApplied++
		}

		// Apply IPv6 security blocking rules
		ipv6Rules := [][]string{
			{"-A", chainName, "-d", "::1/128", "-j", "DROP"},   // IPv6 localhost
			{"-A", chainName, "-d", "fe80::/10", "-j", "DROP"}, // IPv6 link-local
			{"-A", chainName, "-d", "ff00::/8", "-j", "DROP"},  // IPv6 multicast
		}
		for _, rule := range ipv6Rules {
			if err := runIP6Tables(ctx, rule...); err != nil {
				return rulesApplied, err
			}
			rulesApplied++
		}
	}

	// Apply DNS rules for both IPv4 and IPv6
	if policy.AllowDns {
		// Allow DNS queries on UDP/TCP port 53 for both IPv4 and IPv6
		for _, proto := range []string{"udp", "tcp"} {
			if err := runIPTables(ctx, "-A", chainName, "-p", proto, "--dport", "53", "-j", "ACCEPT"); err != nil {
				return rulesApplied, err
			}
			rulesApplied++

			if err := runIP6Tables(ctx, "-A", chainName, "-p", proto, "--dport", "53", "-j", "ACCEPT"); err != nil {
				return rulesApplied, err
			}
			rulesApplied++
		}

		// Allow specific DNS servers if configured
		for _, dns := range policy.DnsServers {
			if _, err := validation.ValidateDNSServer(dns); err != nil {
				return rulesApplied, err
			}

			// Detect IP version and apply to correct chain
			version, err := detectIPVersion(dns)
			if err != nil {
				return rulesApplied, err
			}

			for _, proto := range []string{"udp", "tcp"} {
				if err := runIPTablesForVersion(ctx, version, "-A", chainName, "-d", dns, "-p", proto, "--dport", "53", "-j", "ACCEPT"); err != nil {
					return rulesApplied, err
				}
				rulesApplied++
			}
		}
	}

	if policy.Policy == "deny" && len(policy.Whitelist) > 0 {
		for _, rule := range policy.Whitelist {
			count, err := applyNetworkRule(ctx, chainName, rule, "ACCEPT")
			if err != nil {
				return rulesApplied, err
			}
			rulesApplied += count
		}
	}

	if policy.Policy == "allow" && len(policy.Blacklist) > 0 {
		for _, rule := range policy.Blacklist {
			count, err := applyNetworkRule(ctx, chainName, rule, "DROP")
			if err != nil {
				return rulesApplied, err
			}
			rulesApplied += count
		}
	}

	// Apply default policy as the final rule for both IPv4 and IPv6
	action := "ACCEPT"
	if policy.Policy == "deny" {
		action = "DROP"
	}

	// Apply default policy to IPv4 chain
	if err := runIPTables(ctx, "-A", chainName, "-j", action); err != nil {
		return rulesApplied, err
	}
	rulesApplied++

	// Apply default policy to IPv6 chain
	if err := runIP6Tables(ctx, "-A", chainName, "-j", action); err != nil {
		return rulesApplied, err
	}
	rulesApplied++

	return rulesApplied, nil
}

// applyNetworkRule applies a network rule (whitelist/blacklist) to the appropriate iptables chain.
// It automatically detects IPv4 vs IPv6 and uses the correct iptables command.
func applyNetworkRule(ctx context.Context, chainName string, rule *pb.NetworkRule, action string) (int, error) {
	if _, err := validation.ValidateCIDR(rule.Cidr); err != nil {
		return 0, err
	}

	// Detect IP version (IPv4 or IPv6)
	version, err := detectIPVersion(rule.Cidr)
	if err != nil {
		return 0, err
	}

	rulesApplied := 0

	// Apply rule without port restrictions
	if len(rule.Ports) == 0 {
		if err := runIPTablesForVersion(ctx, version, "-A", chainName, "-d", rule.Cidr, "-j", action); err != nil {
			return rulesApplied, err
		}
		rulesApplied++
	} else {
		// Apply rule with port restrictions
		for _, port := range rule.Ports {
			if err := validation.ValidatePort(port); err != nil {
				return rulesApplied, err
			}

			portStr := fmt.Sprintf("%d", port)
			// Apply for both TCP and UDP protocols
			for _, proto := range []string{"tcp", "udp"} {
				if err := runIPTablesForVersion(ctx, version, "-A", chainName, "-d", rule.Cidr, "-p", proto, "--dport", portStr, "-j", action); err != nil {
					return rulesApplied, err
				}
				rulesApplied++
			}
		}
	}

	return rulesApplied, nil
}

// CleanupChain removes iptables chains for both IPv4 and IPv6.
// It removes FORWARD rules, flushes chain rules, and deletes the chain.
func CleanupChain(ctx context.Context, chainName string, containerIP string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Determine IP version if containerIP is provided
	var version ipVersion = ipv4
	if containerIP != "" {
		detectedVersion, err := detectIPVersion(containerIP)
		if err == nil {
			version = detectedVersion
		}

		// Remove FORWARD rule
		_ = runIPTablesForVersion(ctx, version, "-D", "FORWARD", "-s", containerIP, "-j", chainName)
	}

	// Cleanup both IPv4 and IPv6 chains (one will likely fail, which is fine)
	// This ensures we clean up chains even if we don't know the container IP version
	_ = runIPTables(ctx, "-F", chainName)
	_ = runIPTables(ctx, "-X", chainName)
	_ = runIP6Tables(ctx, "-F", chainName)
	_ = runIP6Tables(ctx, "-X", chainName)

	return nil
}

// detectIPVersion determines if a CIDR or IP address is IPv4 or IPv6
func detectIPVersion(cidr string) (ipVersion, error) {
	// Remove CIDR suffix to parse the IP
	ipStr := cidr
	if strings.Contains(cidr, "/") {
		parts := strings.Split(cidr, "/")
		ipStr = parts[0]
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return 0, fmt.Errorf("invalid IP address: %s", cidr)
	}

	// Check if it's an IPv4 address
	if ip.To4() != nil {
		return ipv4, nil
	}

	return ipv6, nil
}

// runIPTables executes an iptables command (IPv4)
func runIPTables(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "iptables", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables %s failed: %w: %s", strings.Join(args, " "), err, output)
	}
	return nil
}

// runIP6Tables executes an ip6tables command (IPv6)
func runIP6Tables(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "ip6tables", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip6tables %s failed: %w: %s", strings.Join(args, " "), err, output)
	}
	return nil
}

// runIPTablesForVersion executes the appropriate iptables command based on IP version
func runIPTablesForVersion(ctx context.Context, version ipVersion, args ...string) error {
	if version == ipv6 {
		return runIP6Tables(ctx, args...)
	}
	return runIPTables(ctx, args...)
}

type dockerIPAMConfig struct {
	Subnet string `json:"Subnet"`
}

func dockerBridgeSubnets(ctx context.Context) []string {
	defaultSubnets := []string{"172.17.0.0/16"}

	cmd := exec.CommandContext(ctx, "docker", "network", "inspect", "bridge", "--format", "{{json .IPAM.Config}}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return defaultSubnets
	}

	raw := strings.TrimSpace(string(output))
	if raw == "" || raw == "null" {
		return defaultSubnets
	}

	var configs []dockerIPAMConfig
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		return defaultSubnets
	}

	subnets := make([]string, 0, len(configs))
	for _, cfg := range configs {
		if cfg.Subnet != "" {
			subnets = append(subnets, cfg.Subnet)
		}
	}
	if len(subnets) == 0 {
		return defaultSubnets
	}
	return subnets
}
