package config

import (
	"fmt"
	"net"
	"strings"
)

// Hardcoded security rules that CANNOT be disabled or bypassed
const (
	// Localhost ranges - ALWAYS blocked
	LocalhostIPv4 = "127.0.0.0/8"
	LocalhostIPv6 = "::1/128"

	// Cloud provider metadata services - ALWAYS blocked
	CloudMetadata = "169.254.169.254/32" // AWS, GCP, Azure metadata
	LinkLocal     = "169.254.0.0/16"     // Link-local addresses

	// Private IP ranges (RFC 1918) - Blocked unless explicitly whitelisted
	Private10  = "10.0.0.0/8"
	Private172 = "172.16.0.0/12"
	Private192 = "192.168.0.0/16"

	// Other reserved ranges that should be blocked
	Multicast   = "224.0.0.0/4"        // Multicast
	Reserved240 = "240.0.0.0/4"        // Reserved
	Broadcast   = "255.255.255.255/32" // Broadcast
	ZeroConf    = "0.0.0.0/8"          // This network
)

// MandatoryBlockedRanges are ALWAYS blocked and cannot be whitelisted
var MandatoryBlockedRanges = []string{
	LocalhostIPv4,
	LocalhostIPv6,
	CloudMetadata,
	LinkLocal,
	Multicast,
	Reserved240,
	Broadcast,
	ZeroConf,
}

// PrivateRanges are blocked by default but can be whitelisted
var PrivateRanges = []string{
	Private10,
	Private172,
	Private192,
}

// EnforceSecurityRules applies mandatory security rules to a network configuration
// These rules CANNOT be bypassed and are always enforced
func EnforceSecurityRules(cfg *NetworkConfig) error {
	// Ensure BlockMetadata is always enabled
	cfg.BlockMetadata = true

	// Validate whitelist entries don't contain forbidden ranges
	for i, entry := range cfg.Whitelist {
		if err := ValidateWhitelistEntry(&entry); err != nil {
			return fmt.Errorf("whitelist entry %d invalid: %w", i, err)
		}
	}

	// Add mandatory blocked ranges to blacklist
	// These are added unconditionally and cannot be removed
	cfg.Blacklist = append(cfg.Blacklist, createMandatoryBlacklist()...)

	// Add private ranges to blacklist if not explicitly whitelisted
	privateBlocks := filterPrivateRanges(cfg.Whitelist)
	cfg.Blacklist = append(cfg.Blacklist, privateBlocks...)

	// Deduplicate blacklist
	cfg.Blacklist = deduplicateBlacklist(cfg.Blacklist)

	return nil
}

// ValidateWhitelistEntry ensures a whitelist entry doesn't contain forbidden IP ranges
func ValidateWhitelistEntry(entry *WhitelistEntry) error {
	if entry.CIDR == "" {
		return fmt.Errorf("CIDR cannot be empty")
	}

	// Parse the CIDR
	_, entryNet, err := net.ParseCIDR(entry.CIDR)
	if err != nil {
		return fmt.Errorf("invalid CIDR format '%s': %w", entry.CIDR, err)
	}

	// Allow 0.0.0.0/0 as a special case - it means "allow all public internet"
	// The iptables rules will still enforce blocks for forbidden ranges
	if entry.CIDR == "0.0.0.0/0" || entry.CIDR == "::/0" {
		return nil
	}

	// Check against mandatory blocked ranges
	for _, blockedCIDR := range MandatoryBlockedRanges {
		_, blockedNet, err := net.ParseCIDR(blockedCIDR)
		if err != nil {
			continue // Should never happen with hardcoded values
		}

		if networksOverlap(entryNet, blockedNet) {
			return fmt.Errorf(
				"CIDR '%s' overlaps with forbidden range '%s' (localhost, metadata, or reserved)",
				entry.CIDR, blockedCIDR,
			)
		}
	}

	// Validate ports (supports both single ports and ranges like "80-443")
	for _, port := range entry.Ports {
		// Check if it's a port range
		if strings.Contains(port, "-") {
			var startPort, endPort uint32
			if _, err := fmt.Sscanf(port, "%d-%d", &startPort, &endPort); err != nil {
				return fmt.Errorf("invalid port range '%s': %w", port, err)
			}
			if startPort == 0 || startPort > 65535 {
				return fmt.Errorf("port range start %d out of valid range (1-65535)", startPort)
			}
			if endPort == 0 || endPort > 65535 {
				return fmt.Errorf("port range end %d out of valid range (1-65535)", endPort)
			}
			if endPort <= startPort {
				return fmt.Errorf("port range end %d must be greater than start %d", endPort, startPort)
			}
		} else {
			// Single port
			var portNum uint32
			if _, err := fmt.Sscanf(port, "%d", &portNum); err != nil {
				return fmt.Errorf("invalid port '%s': %w", port, err)
			}
			if portNum == 0 || portNum > 65535 {
				return fmt.Errorf("port %d out of valid range (1-65535)", portNum)
			}
		}
	}

	return nil
}

// networksOverlap checks if two IP networks overlap
func networksOverlap(net1, net2 *net.IPNet) bool {
	// Check if either network contains the first IP of the other
	return net1.Contains(net2.IP) || net2.Contains(net1.IP)
}

// createMandatoryBlacklist creates blacklist entries for ranges that must always be blocked
func createMandatoryBlacklist() []BlacklistEntry {
	entries := make([]BlacklistEntry, 0, len(MandatoryBlockedRanges))

	descriptions := map[string]string{
		LocalhostIPv4: "Localhost (MANDATORY BLOCK)",
		LocalhostIPv6: "Localhost IPv6 (MANDATORY BLOCK)",
		CloudMetadata: "Cloud provider metadata service (MANDATORY BLOCK)",
		LinkLocal:     "Link-local addresses (MANDATORY BLOCK)",
		Multicast:     "Multicast addresses (MANDATORY BLOCK)",
		Reserved240:   "Reserved addresses (MANDATORY BLOCK)",
		Broadcast:     "Broadcast address (MANDATORY BLOCK)",
		ZeroConf:      "Zero configuration network (MANDATORY BLOCK)",
	}

	for _, cidr := range MandatoryBlockedRanges {
		desc := descriptions[cidr]
		if desc == "" {
			desc = "Mandatory security block"
		}
		entries = append(entries, BlacklistEntry{
			CIDR:        cidr,
			Description: desc,
		})
	}

	return entries
}

// filterPrivateRanges returns private IP ranges that are NOT in the whitelist
func filterPrivateRanges(whitelist []WhitelistEntry) []BlacklistEntry {
	blocked := make([]BlacklistEntry, 0)

	for _, privateRange := range PrivateRanges {
		_, privateNet, err := net.ParseCIDR(privateRange)
		if err != nil {
			continue // Should never happen with hardcoded values
		}

		// Check if this private range is whitelisted
		whitelisted := false
		for _, entry := range whitelist {
			_, entryNet, err := net.ParseCIDR(entry.CIDR)
			if err != nil {
				continue
			}

			// If whitelist entry overlaps with this private range, allow it
			if networksOverlap(privateNet, entryNet) {
				whitelisted = true
				break
			}
		}

		// If not whitelisted, add to blacklist
		if !whitelisted {
			desc := fmt.Sprintf("Private IP range %s (blocked unless whitelisted)", privateRange)
			blocked = append(blocked, BlacklistEntry{
				CIDR:        privateRange,
				Description: desc,
			})
		}
	}

	return blocked
}

// deduplicateBlacklist removes duplicate CIDR entries from blacklist
func deduplicateBlacklist(blacklist []BlacklistEntry) []BlacklistEntry {
	seen := make(map[string]bool)
	result := make([]BlacklistEntry, 0, len(blacklist))

	for _, entry := range blacklist {
		// Normalize CIDR for comparison
		cidr := strings.ToLower(strings.TrimSpace(entry.CIDR))
		if !seen[cidr] {
			seen[cidr] = true
			result = append(result, entry)
		}
	}

	return result
}

// IsPublicIP checks if an IP address is a public (non-private) IP
func IsPublicIP(ip net.IP) bool {
	// Check against all private and reserved ranges
	allReserved := append(MandatoryBlockedRanges, PrivateRanges...)

	for _, cidr := range allReserved {
		_, reservedNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if reservedNet.Contains(ip) {
			return false
		}
	}

	return true
}

// ValidateNetworkConfig performs comprehensive validation of network configuration
func ValidateNetworkConfig(cfg *NetworkConfig) error {
	if cfg == nil {
		return fmt.Errorf("network config cannot be nil")
	}

	// Enforce security rules (this modifies the config in place)
	if err := EnforceSecurityRules(cfg); err != nil {
		return fmt.Errorf("failed to enforce security rules: %w", err)
	}

	// Validate default policy
	policy := strings.ToLower(cfg.DefaultPolicy)
	if policy != "allow" && policy != "deny" {
		return fmt.Errorf("default policy must be 'allow' or 'deny', got '%s'", cfg.DefaultPolicy)
	}

	// Validate DNS servers
	for i, dns := range cfg.DNSServers {
		ip := net.ParseIP(dns)
		if ip == nil {
			return fmt.Errorf("DNS server %d has invalid IP address: %s", i, dns)
		}

		// Ensure DNS servers are not localhost or metadata services
		for _, blockedCIDR := range MandatoryBlockedRanges {
			_, blockedNet, err := net.ParseCIDR(blockedCIDR)
			if err != nil {
				continue
			}
			if blockedNet.Contains(ip) {
				return fmt.Errorf("DNS server %d (%s) is in a forbidden range (%s)", i, dns, blockedCIDR)
			}
		}
	}

	return nil
}
