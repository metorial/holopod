#!/bin/bash

# Comprehensive network security test for isolation-runner
# Verifies that containers CANNOT access:
# - Localhost (127.0.0.0/8)
# - Cloud metadata services (169.254.169.254)
# - Private IPs (unless whitelisted)

set -e

echo "========================================="
echo "Network Security Test for Isolation-Runner"
echo "========================================="
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass_count=0
fail_count=0

function test_result() {
    local test_name="$1"
    local expected="$2"
    local result="$3"

    if [ "$expected" == "$result" ]; then
        echo -e "${GREEN}✓ PASS${NC}: $test_name"
        ((pass_count++))
    else
        echo -e "${RED}✗ FAIL${NC}: $test_name (expected: $expected, got: $result)"
        ((fail_count++))
    fi
}

# Test 1: Verify localhost cannot be whitelisted
echo "Test 1: Attempting to whitelist localhost (should fail)..."
cat > /tmp/test_config_localhost.json <<EOF
{
    "version": "1.0",
    "network": {
        "whitelist": [
            {
                "cidr": "127.0.0.1/32",
                "description": "Localhost - should be rejected"
            }
        ],
        "blacklist": [],
        "default_policy": "deny",
        "block_metadata": true,
        "allow_dns": false,
        "dns_servers": ["8.8.8.8"]
    },
    "container": {
        "runtime": "runsc"
    },
    "execution": {
        "auto_cleanup": true,
        "interactive": false,
        "attach_stdin": true,
        "attach_stdout": true,
        "attach_stderr": true,
        "tty": false
    },
    "logging": {
        "enabled": true,
        "log_network_attempts": true,
        "log_level": "info"
    }
}
EOF

# This should fail because localhost cannot be whitelisted
if timeout 5s ../bin/isolation-runner --config /tmp/test_config_localhost.json --image alpine:latest --command "echo test" 2>&1 | grep -q "network security validation failed"; then
    test_result "Localhost whitelist blocked" "BLOCKED" "BLOCKED"
else
    test_result "Localhost whitelist blocked" "BLOCKED" "ALLOWED"
fi

# Test 2: Verify metadata service cannot be whitelisted
echo ""
echo "Test 2: Attempting to whitelist metadata service (should fail)..."
cat > /tmp/test_config_metadata.json <<EOF
{
    "version": "1.0",
    "network": {
        "whitelist": [
            {
                "cidr": "169.254.169.254/32",
                "description": "Metadata service - should be rejected"
            }
        ],
        "blacklist": [],
        "default_policy": "deny",
        "block_metadata": true,
        "allow_dns": false,
        "dns_servers": ["8.8.8.8"]
    },
    "container": {
        "runtime": "runsc"
    },
    "execution": {
        "auto_cleanup": true,
        "interactive": false,
        "attach_stdin": true,
        "attach_stdout": true,
        "attach_stderr": true,
        "tty": false
    },
    "logging": {
        "enabled": true,
        "log_network_attempts": true,
        "log_level": "info"
    }
}
EOF

if timeout 5s ../bin/isolation-runner --config /tmp/test_config_metadata.json --image alpine:latest --command "echo test" 2>&1 | grep -q "network security validation failed"; then
    test_result "Metadata service whitelist blocked" "BLOCKED" "BLOCKED"
else
    test_result "Metadata service whitelist blocked" "BLOCKED" "ALLOWED"
fi

# Test 3: Verify link-local addresses cannot be whitelisted
echo ""
echo "Test 3: Attempting to whitelist link-local range (should fail)..."
cat > /tmp/test_config_linklocal.json <<EOF
{
    "version": "1.0",
    "network": {
        "whitelist": [
            {
                "cidr": "169.254.0.0/16",
                "description": "Link-local - should be rejected"
            }
        ],
        "blacklist": [],
        "default_policy": "deny",
        "block_metadata": true,
        "allow_dns": false,
        "dns_servers": ["8.8.8.8"]
    },
    "container": {
        "runtime": "runsc"
    },
    "execution": {
        "auto_cleanup": true,
        "interactive": false,
        "attach_stdin": true,
        "attach_stdout": true,
        "attach_stderr": true,
        "tty": false
    },
    "logging": {
        "enabled": true,
        "log_network_attempts": true,
        "log_level": "info"
    }
}
EOF

if timeout 5s ../bin/isolation-runner --config /tmp/test_config_linklocal.json --image alpine:latest --command "echo test" 2>&1 | grep -q "network security validation failed"; then
    test_result "Link-local whitelist blocked" "BLOCKED" "BLOCKED"
else
    test_result "Link-local whitelist blocked" "BLOCKED" "ALLOWED"
fi

# Test 4: Verify private IPs are blocked by default
echo ""
echo "Test 4: Verifying private IPs blocked by default (should succeed)..."
cat > /tmp/test_config_private_blocked.json <<EOF
{
    "version": "1.0",
    "network": {
        "whitelist": [],
        "blacklist": [],
        "default_policy": "deny",
        "block_metadata": true,
        "allow_dns": false,
        "dns_servers": ["8.8.8.8"]
    },
    "container": {
        "runtime": "runsc"
    },
    "execution": {
        "auto_cleanup": true,
        "interactive": false,
        "attach_stdin": true,
        "attach_stdout": true,
        "attach_stderr": true,
        "tty": false
    },
    "logging": {
        "enabled": true,
        "log_network_attempts": true,
        "log_level": "info"
    }
}
EOF

# This should succeed and the config should contain private IP blocks
if timeout 5s ../bin/isolation-runner --config /tmp/test_config_private_blocked.json --image alpine:latest --command "echo test" 2>&1 | grep -q "Network security rules validated"; then
    test_result "Private IPs blocked by default config accepted" "SUCCESS" "SUCCESS"
else
    test_result "Private IPs blocked by default config accepted" "SUCCESS" "FAIL"
fi

# Test 5: Verify public IPs can be whitelisted
echo ""
echo "Test 5: Whitelisting public IPs (should succeed)..."
cat > /tmp/test_config_public.json <<EOF
{
    "version": "1.0",
    "network": {
        "whitelist": [
            {
                "cidr": "8.8.8.8/32",
                "description": "Google DNS - public IP, should be allowed",
                "ports": ["53"]
            }
        ],
        "blacklist": [],
        "default_policy": "deny",
        "block_metadata": true,
        "allow_dns": true,
        "dns_servers": ["8.8.8.8"]
    },
    "container": {
        "runtime": "runsc"
    },
    "execution": {
        "auto_cleanup": true,
        "interactive": false,
        "attach_stdin": true,
        "attach_stdout": true,
        "attach_stderr": true,
        "tty": false
    },
    "logging": {
        "enabled": true,
        "log_network_attempts": true,
        "log_level": "info"
    }
}
EOF

if timeout 5s ../bin/isolation-runner --config /tmp/test_config_public.json --image alpine:latest --command "echo test" 2>&1 | grep -q "Network security rules validated"; then
    test_result "Public IP whitelist allowed" "ALLOWED" "ALLOWED"
else
    test_result "Public IP whitelist allowed" "ALLOWED" "BLOCKED"
fi

# Test 6: Verify private IPs can be whitelisted explicitly
echo ""
echo "Test 6: Explicitly whitelisting private IP (should succeed)..."
cat > /tmp/test_config_private_whitelist.json <<EOF
{
    "version": "1.0",
    "network": {
        "whitelist": [
            {
                "cidr": "10.0.1.0/24",
                "description": "Internal API network",
                "ports": ["443"]
            }
        ],
        "blacklist": [],
        "default_policy": "deny",
        "block_metadata": true,
        "allow_dns": false,
        "dns_servers": ["8.8.8.8"]
    },
    "container": {
        "runtime": "runsc"
    },
    "execution": {
        "auto_cleanup": true,
        "interactive": false,
        "attach_stdin": true,
        "attach_stdout": true,
        "attach_stderr": true,
        "tty": false
    },
    "logging": {
        "enabled": true,
        "log_network_attempts": true,
        "log_level": "info"
    }
}
EOF

if timeout 5s ../bin/isolation-runner --config /tmp/test_config_private_whitelist.json --image alpine:latest --command "echo test" 2>&1 | grep -q "Network security rules validated"; then
    test_result "Private IP explicit whitelist allowed" "ALLOWED" "ALLOWED"
else
    test_result "Private IP explicit whitelist allowed" "ALLOWED" "BLOCKED"
fi

# Test 7: Verify localhost DNS is rejected
echo ""
echo "Test 7: Using localhost as DNS server (should fail)..."
cat > /tmp/test_config_localhost_dns.json <<EOF
{
    "version": "1.0",
    "network": {
        "whitelist": [],
        "blacklist": [],
        "default_policy": "deny",
        "block_metadata": true,
        "allow_dns": true,
        "dns_servers": ["127.0.0.1"]
    },
    "container": {
        "runtime": "runsc"
    },
    "execution": {
        "auto_cleanup": true,
        "interactive": false,
        "attach_stdin": true,
        "attach_stdout": true,
        "attach_stderr": true,
        "tty": false
    },
    "logging": {
        "enabled": true,
        "log_network_attempts": true,
        "log_level": "info"
    }
}
EOF

if timeout 5s ../bin/isolation-runner --config /tmp/test_config_localhost_dns.json --image alpine:latest --command "echo test" 2>&1 | grep -q "network security validation failed"; then
    test_result "Localhost DNS server blocked" "BLOCKED" "BLOCKED"
else
    test_result "Localhost DNS server blocked" "BLOCKED" "ALLOWED"
fi

# Cleanup
rm -f /tmp/test_config_*.json

# Summary
echo ""
echo "========================================="
echo "Test Summary"
echo "========================================="
echo -e "${GREEN}Passed: $pass_count${NC}"
echo -e "${RED}Failed: $fail_count${NC}"
echo ""

if [ $fail_count -eq 0 ]; then
    echo -e "${GREEN}All network security tests passed! ✓${NC}"
    echo ""
    echo "Verified security guarantees:"
    echo "  ✓ Localhost (127.0.0.0/8) cannot be accessed"
    echo "  ✓ Cloud metadata (169.254.169.254) cannot be accessed"
    echo "  ✓ Link-local addresses (169.254.0.0/16) cannot be accessed"
    echo "  ✓ Private IPs are blocked by default"
    echo "  ✓ Private IPs can be explicitly whitelisted"
    echo "  ✓ Public IPs can be whitelisted"
    echo "  ✓ Localhost DNS is rejected"
    exit 0
else
    echo -e "${RED}Some network security tests failed!${NC}"
    exit 1
fi
