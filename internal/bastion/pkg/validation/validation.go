package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strings"
)

var (
	chainNameRegex = regexp.MustCompile(`^ISO-[a-f0-9]{16}$`)
)

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func ValidateChainName(chainName string) error {
	if len(chainName) > 28 {
		return ValidationError{
			Field:   "chain_name",
			Message: fmt.Sprintf("chain name too long (max 28 chars): %s", chainName),
		}
	}

	if !chainNameRegex.MatchString(chainName) {
		return ValidationError{
			Field:   "chain_name",
			Message: fmt.Sprintf("chain name must match pattern ISO-[a-f0-9]{16}, got: %s", chainName),
		}
	}

	return nil
}

func ValidateContainerIP(ipStr string) (net.IP, error) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, ValidationError{
			Field:   "container_ip",
			Message: fmt.Sprintf("invalid IP address: %s", ipStr),
		}
	}

	ip = ip.To4()
	if ip == nil {
		return nil, ValidationError{
			Field:   "container_ip",
			Message: "only IPv4 addresses supported",
		}
	}

	if !isPrivateIP(ip) {
		return nil, ValidationError{
			Field:   "container_ip",
			Message: fmt.Sprintf("IP address is not private (RFC1918): %s", ipStr),
		}
	}

	return ip, nil
}

func isPrivateIP(ip net.IP) bool {
	if ip[0] == 10 {
		return true
	}
	if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
		return true
	}
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	return false
}

func ValidateCIDR(cidr string) (*net.IPNet, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, ValidationError{
			Field:   "cidr",
			Message: fmt.Sprintf("invalid CIDR notation: %s", cidr),
		}
	}
	return ipNet, nil
}

func ValidatePort(port uint32) error {
	if port == 0 || port > 65535 {
		return ValidationError{
			Field:   "port",
			Message: fmt.Sprintf("invalid port number: %d (must be 1-65535)", port),
		}
	}
	return nil
}

func ValidateDNSServer(dnsIP string) (net.IP, error) {
	ip := net.ParseIP(dnsIP)
	if ip == nil {
		return nil, ValidationError{
			Field:   "dns_server",
			Message: fmt.Sprintf("invalid DNS server IP: %s", dnsIP),
		}
	}

	if ip.IsLoopback() {
		return nil, ValidationError{
			Field:   "dns_server",
			Message: fmt.Sprintf("loopback DNS servers not allowed: %s", dnsIP),
		}
	}

	ip4 := ip.To4()
	if ip4 != nil {
		if ip4[0] == 169 && ip4[1] == 254 {
			return nil, ValidationError{
				Field:   "dns_server",
				Message: fmt.Sprintf("link-local DNS servers not allowed: %s", dnsIP),
			}
		}

		if dnsIP == "169.254.169.254" || dnsIP == "168.63.129.16" || dnsIP == "100.100.100.200" {
			return nil, ValidationError{
				Field:   "dns_server",
				Message: fmt.Sprintf("cloud metadata IPs not allowed as DNS servers: %s", dnsIP),
			}
		}
	}

	return ip, nil
}

func ValidatePolicyMode(policy string) error {
	if policy != "allow" && policy != "deny" {
		return ValidationError{
			Field:   "policy",
			Message: fmt.Sprintf("policy must be 'allow' or 'deny', got: %s", policy),
		}
	}
	return nil
}

func ValidateNetworkName(name string) error {
	if !strings.HasPrefix(name, "iso-net-") {
		return ValidationError{
			Field:   "network_name",
			Message: "network name must start with 'iso-net-'",
		}
	}

	for _, ch := range name {
		if !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') && ch != '-' {
			return ValidationError{
				Field:   "network_name",
				Message: "network name contains invalid characters (only alphanumeric and '-' allowed)",
			}
		}
	}

	if len(name) < 10 {
		return ValidationError{
			Field:   "network_name",
			Message: "network name too short (min 10 characters)",
		}
	}
	if len(name) > 64 {
		return ValidationError{
			Field:   "network_name",
			Message: "network name too long (max 64 characters)",
		}
	}

	return nil
}

func ValidateConfigHash(providedHash string, subnetRange *string, minIPs uint32, driver string) error {
	hasher := sha256.New()

	if subnetRange != nil {
		hasher.Write([]byte(*subnetRange))
	}

	minIPsBytes := []byte{
		byte(minIPs),
		byte(minIPs >> 8),
		byte(minIPs >> 16),
		byte(minIPs >> 24),
	}
	hasher.Write(minIPsBytes)
	hasher.Write([]byte(driver))

	expectedHash := hex.EncodeToString(hasher.Sum(nil))

	if providedHash != expectedHash {
		return ValidationError{
			Field:   "config_hash",
			Message: fmt.Sprintf("config hash mismatch: expected %s, got %s", expectedHash, providedHash),
		}
	}

	return nil
}

func ValidateContainerID(containerID string) error {
	if containerID == "" {
		return ValidationError{
			Field:   "container_id",
			Message: "container ID cannot be empty",
		}
	}

	if len(containerID) < 12 || len(containerID) > 64 {
		return ValidationError{
			Field:   "container_id",
			Message: fmt.Sprintf("container ID has invalid length: %d (expected 12-64 characters)", len(containerID)),
		}
	}

	for _, ch := range containerID {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || ch == '-' || ch == '_') {
			return ValidationError{
				Field:   "container_id",
				Message: "container ID must contain only lowercase alphanumeric characters, hyphens, or underscores",
			}
		}
	}

	return nil
}
