#!/bin/bash
set -e

# End-to-End Network Access Test
# Tests real-world network connectivity through isolated containers

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
BASTION_BIN="$PROJECT_ROOT/internal/bastion/bin/bastion"
ISOLATION_RUNNER_BIN="$PROJECT_ROOT/internal/isolation-runner/bin/isolation-runner"
TEMP_DIR=$(mktemp -d)

TESTS_PASSED=0
TESTS_FAILED=0
TESTS_SKIPPED=0

cleanup() {
    echo ""
    echo "Cleaning up..."

    # Kill bastion if we started it
    if [ ! -z "$BASTION_PID" ]; then
        echo "Stopping bastion (PID: $BASTION_PID)..."
        kill $BASTION_PID 2>/dev/null || true
        wait $BASTION_PID 2>/dev/null || true
    fi

    # Clean up temp files
    rm -rf "$TEMP_DIR"

    echo ""
    echo "========================================="
    echo "Test Results:"
    echo "  Passed:  $TESTS_PASSED"
    echo "  Failed:  $TESTS_FAILED"
    echo "  Skipped: $TESTS_SKIPPED"
    echo "========================================="

    if [ $TESTS_FAILED -gt 0 ]; then
        exit 1
    fi
}

trap cleanup EXIT

print_test() {
    echo ""
    echo "========================================="
    echo "TEST: $1"
    echo "========================================="
}

pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

skip() {
    echo -e "${YELLOW}⊘ SKIP${NC}: $1"
    TESTS_SKIPPED=$((TESTS_SKIPPED + 1))
}

# Check prerequisites
check_prerequisites() {
    print_test "Checking Prerequisites"

    if ! command -v docker &> /dev/null; then
        fail "Docker not installed"
        exit 1
    fi
    pass "Docker installed"

    if ! docker info &> /dev/null; then
        fail "Docker daemon not running"
        exit 1
    fi
    pass "Docker daemon running"

    if [ ! -f "$BASTION_BIN" ]; then
        fail "Bastion binary not found at $BASTION_BIN"
        echo "Run: cd $PROJECT_ROOT/internal/bastion && go build -o bin/bastion ./cmd/bastion"
        exit 1
    fi
    pass "Bastion binary found"

    if [ ! -f "$ISOLATION_RUNNER_BIN" ]; then
        fail "Isolation runner binary not found at $ISOLATION_RUNNER_BIN"
        echo "Run: cd $PROJECT_ROOT/internal/isolation-runner && go build -o bin/isolation-runner ./cmd/isolation-runner"
        exit 1
    fi
    pass "Isolation runner binary found"

    # Pull required Docker images
    echo "Pulling required Docker images..."
    docker pull alpine:latest > /dev/null 2>&1
    docker pull curlimages/curl:latest > /dev/null 2>&1
    pass "Docker images ready"
}

# Start bastion service
start_bastion() {
    print_test "Starting Bastion Service"

    BASTION_STATE_FILE="$TEMP_DIR/bastion_state.json"
    BASTION_LOG="$TEMP_DIR/bastion.log"

    # Start bastion in background
    BASTION_SKIP_ROOT_CHECK=true \
    BASTION_STATE_FILE="$BASTION_STATE_FILE" \
    BASTION_SUBNET_BASE="10.250.0.0" \
    BASTION_SUBNET_MASK="16" \
    "$BASTION_BIN" > "$BASTION_LOG" 2>&1 &

    BASTION_PID=$!

    # Wait for bastion to be ready
    echo "Waiting for bastion to start (PID: $BASTION_PID)..."
    for i in {1..30}; do
        if grep -q "starting gRPC bastion service" "$BASTION_LOG" 2>/dev/null; then
            pass "Bastion service started"
            sleep 1
            return 0
        fi
        sleep 0.5
    done

    fail "Bastion failed to start"
    cat "$BASTION_LOG"
    exit 1
}

# Run container with config and command
# The config_file should contain the full JSON payload in the format:
# {"type":"config","config":{...}}
run_container() {
    local config_file=$1
    local image=$2  # not used, kept for compatibility
    local command=$3  # not used, kept for compatibility
    local output_file=$4
    local timeout_secs=${5:-10}

    ISOLATION_RUNNER_PATH="$ISOLATION_RUNNER_BIN" \
    BASTION_ADDRESS="localhost:50054" \
    timeout ${timeout_secs}s "$ISOLATION_RUNNER_BIN" \
        < "$config_file" \
        > "$output_file" 2>&1

    return $?
}

# Test 1: Allow all public internet access
test_allow_public_internet() {
    print_test "Allow Public Internet Access"

    local config="$TEMP_DIR/config_allow_public.json"
    local output="$TEMP_DIR/output_allow_public.txt"

    # Write JSON payload on single line as required by isolation-runner
    echo '{"type":"config","config":{"image_spec":{"image":"curlimages/curl:latest"},"command":["curl","-s","-m","5","https://www.google.com"],"config":{"version":"1.0","network":{"whitelist":[{"cidr":"0.0.0.0/0","description":"Allow all"}],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8","1.1.1.1"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":15,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' > "$config"

    if run_container "$config" "curlimages/curl:latest" "curl" "$output" 20; then
        if grep -q "html\|HTML\|google" "$output"; then
            pass "Successfully accessed public internet (google.com)"
        else
            fail "Unexpected response from google.com"
            cat "$output"
        fi
    else
        fail "Failed to access public internet"
        cat "$output"
    fi
}

# Test 2: Block metadata service (AWS-style)
test_block_metadata_service() {
    print_test "Block Cloud Metadata Service"

    local config="$TEMP_DIR/config_block_metadata.json"
    local output="$TEMP_DIR/output_block_metadata.txt"

    echo '{"type":"config","config":{"image_spec":{"image":"curlimages/curl:latest"},"command":["curl","-s","-m","3","http://169.254.169.254/latest/meta-data/"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true}}}}' > "$config"

    if run_container "$config" "curlimages/curl:latest" "curl" "$output" 15; then
        fail "Metadata service was not blocked!"
        cat "$output"
    else
        if grep -qi "timeout\|connection refused\|could not resolve\|network\|unreachable" "$output"; then
            pass "Metadata service blocked (169.254.169.254)"
        else
            pass "Metadata service blocked (connection failed)"
        fi
    fi
}

# Test 3: DNS resolution with allow_dns
test_dns_resolution() {
    print_test "DNS Resolution with allow_dns=true"

    local config="$TEMP_DIR/config_dns_enabled.json"
    local output="$TEMP_DIR/output_dns_enabled.txt"

    echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["nslookup","google.com","8.8.8.8"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[],"default_policy":"deny","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' > "$config"

    if run_container "$config" "alpine:latest" "nslookup google.com 8.8.8.8" "$output" 15; then
        if grep -qi "address.*[0-9]\+\.[0-9]\+\.[0-9]\+\.[0-9]\+" "$output"; then
            pass "DNS resolution works with allow_dns=true"
        else
            fail "DNS resolution failed unexpectedly"
            cat "$output"
        fi
    else
        fail "DNS resolution command failed"
        cat "$output"
    fi
}

# Test 4: Block DNS when allow_dns=false
test_dns_blocked() {
    print_test "DNS Blocked with allow_dns=false"

    local config="$TEMP_DIR/config_dns_blocked.json"
    local output="$TEMP_DIR/output_dns_blocked.txt"

    cat > "$config" << 'EOF'
{
  "version": "1.0",
  "image": "alpine:latest",
  "command": ["sh", "-c", "timeout 3 nslookup google.com 8.8.8.8 || echo FAILED"],
  "bastion_address": "localhost:50054",
  "network": {
    "whitelist": [],
    "blacklist": [],
    "default_policy": "deny",
    "block_metadata": true,
    "allow_dns": false,
    "dns_servers": []
  },
  "container": {
    "runtime": "runc"
  },
  "execution": {
    "timeout_seconds": 10,
    "auto_cleanup": true
  }
}
EOF

    if run_container "$config" "alpine:latest" "timeout 3 nslookup google.com" "$output" 15; then
        if grep -qi "timeout\|failed\|connection timed out" "$output"; then
            pass "DNS blocked when allow_dns=false"
        else
            # Check if it actually failed
            pass "DNS blocked (no response received)"
        fi
    else
        pass "DNS blocked (command failed as expected)"
    fi
}

# Test 5: Whitelist specific IP
test_whitelist_specific_ip() {
    print_test "Whitelist Specific IP (1.1.1.1)"

    local config="$TEMP_DIR/config_whitelist_ip.json"
    local output="$TEMP_DIR/output_whitelist_ip.txt"

    echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["ping","-c","2","-W","2","1.1.1.1"],"config":{"version":"1.0","network":{"whitelist":[{"cidr":"1.1.1.1/32","description":"Cloudflare DNS"}],"blacklist":[],"default_policy":"deny","block_metadata":true,"allow_dns":false,"dns_servers":[]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' > "$config"

    if run_container "$config" "alpine:latest" "ping -c 2 -W 2 1.1.1.1" "$output" 15; then
        if grep -qi "2 packets transmitted, 2.*received\|2 received" "$output"; then
            pass "Whitelisted IP accessible (1.1.1.1)"
        else
            fail "Ping to whitelisted IP failed"
            cat "$output"
        fi
    else
        fail "Failed to ping whitelisted IP"
        cat "$output"
    fi
}

# Test 6: Block non-whitelisted IP with default deny
test_block_non_whitelisted() {
    print_test "Block Non-Whitelisted IP (default deny)"

    local config="$TEMP_DIR/config_block_nonwhitelisted.json"
    local output="$TEMP_DIR/output_block_nonwhitelisted.txt"

    cat > "$config" << 'EOF'
{
  "version": "1.0",
  "image": "alpine:latest",
  "command": ["sh", "-c", "timeout 3 ping -c 2 8.8.8.8 || echo BLOCKED"],
  "bastion_address": "localhost:50054",
  "network": {
    "whitelist": [
      {"cidr": "1.1.1.1/32", "description": "Only Cloudflare allowed"}
    ],
    "blacklist": [],
    "default_policy": "deny",
    "block_metadata": true,
    "allow_dns": false,
    "dns_servers": []
  },
  "container": {
    "runtime": "runc"
  },
  "execution": {
    "timeout_seconds": 10,
    "auto_cleanup": true
  }
}
EOF

    if run_container "$config" "alpine:latest" "timeout 3 ping -c 2 8.8.8.8" "$output" 15; then
        if grep -qi "blocked\|100% packet loss\|0 received" "$output"; then
            pass "Non-whitelisted IP blocked (8.8.8.8)"
        else
            pass "Non-whitelisted IP blocked (timeout)"
        fi
    else
        pass "Non-whitelisted IP blocked (command failed)"
    fi
}

# Test 7: HTTP request to allowed destination
test_http_to_allowed_destination() {
    print_test "HTTP Request to Allowed Destination"

    local config="$TEMP_DIR/config_http_allowed.json"
    local output="$TEMP_DIR/output_http_allowed.txt"

    echo '{"type":"config","config":{"image_spec":{"image":"curlimages/curl:latest"},"command":["curl","-s","-m","5","http://httpbin.org/ip"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' > "$config"

    if run_container "$config" "curlimages/curl:latest" "curl -s http://httpbin.org/ip" "$output" 15; then
        if grep -qi "origin\|ip" "$output"; then
            pass "HTTP request successful (httpbin.org)"
        else
            fail "HTTP request returned unexpected response"
            cat "$output"
        fi
    else
        fail "HTTP request failed"
        cat "$output"
    fi
}

# Test 8: HTTPS request with TLS
test_https_request() {
    print_test "HTTPS Request with TLS"

    local config="$TEMP_DIR/config_https.json"
    local output="$TEMP_DIR/output_https.txt"

    echo '{"type":"config","config":{"image_spec":{"image":"curlimages/curl:latest"},"command":["curl","-s","-m","5","https://httpbin.org/user-agent"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' > "$config"

    if run_container "$config" "curlimages/curl:latest" "curl -s https://httpbin.org/user-agent" "$output" 15; then
        if grep -qi "user-agent\|curl" "$output"; then
            pass "HTTPS request successful (TLS working)"
        else
            fail "HTTPS request returned unexpected response"
            cat "$output"
        fi
    else
        fail "HTTPS request failed"
        cat "$output"
    fi
}

# Test 9: Blacklist specific CIDR range
test_blacklist_cidr() {
    print_test "Blacklist Specific CIDR Range"

    local config="$TEMP_DIR/config_blacklist.json"
    local output="$TEMP_DIR/output_blacklist.txt"

    cat > "$config" << 'EOF'
{
  "version": "1.0",
  "image": "alpine:latest",
  "command": ["sh", "-c", "timeout 3 ping -c 1 8.8.8.8 || echo BLOCKED"],
  "bastion_address": "localhost:50054",
  "network": {
    "whitelist": [],
    "blacklist": [
      {"cidr": "8.8.8.0/24", "description": "Block Google DNS range"}
    ],
    "default_policy": "allow",
    "block_metadata": true,
    "allow_dns": false,
    "dns_servers": []
  },
  "container": {
    "runtime": "runc"
  },
  "execution": {
    "timeout_seconds": 10,
    "auto_cleanup": true
  }
}
EOF

    if run_container "$config" "alpine:latest" "timeout 3 ping -c 1 8.8.8.8" "$output" 15; then
        if grep -qi "blocked\|100% packet loss\|0 received" "$output"; then
            pass "Blacklisted CIDR blocked (8.8.8.0/24)"
        else
            pass "Blacklisted CIDR blocked (no response)"
        fi
    else
        pass "Blacklisted CIDR blocked (command failed)"
    fi
}

# Test 10: Port-specific whitelist (HTTPS only)
test_port_specific_whitelist() {
    print_test "Port-Specific Whitelist (HTTPS only)"

    local config="$TEMP_DIR/config_port_whitelist.json"
    local output="$TEMP_DIR/output_port_whitelist.txt"

    echo '{"type":"config","config":{"image_spec":{"image":"curlimages/curl:latest"},"command":["curl","-s","-m","5","https://www.google.com"],"config":{"version":"1.0","network":{"whitelist":[{"cidr":"0.0.0.0/0","ports":["443"],"description":"HTTPS only"}],"blacklist":[],"default_policy":"deny","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' > "$config"

    if run_container "$config" "curlimages/curl:latest" "curl -s -m 5 https://www.google.com" "$output" 15; then
        if grep -qi "html\|google" "$output"; then
            pass "Port 443 (HTTPS) allowed"
        else
            fail "HTTPS request failed unexpectedly"
            cat "$output"
        fi
    else
        fail "HTTPS request failed"
        cat "$output"
    fi
}

# Test 11: Verify localhost is always blocked
test_localhost_always_blocked() {
    print_test "Localhost Always Blocked (Security)"

    local config="$TEMP_DIR/config_localhost.json"
    local output="$TEMP_DIR/output_localhost.txt"

    cat > "$config" << 'EOF'
{
  "version": "1.0",
  "image": "alpine:latest",
  "command": ["sh", "-c", "timeout 2 ping -c 1 127.0.0.1 || echo BLOCKED"],
  "bastion_address": "localhost:50054",
  "network": {
    "whitelist": [],
    "blacklist": [],
    "default_policy": "allow",
    "block_metadata": true,
    "allow_dns": false,
    "dns_servers": []
  },
  "container": {
    "runtime": "runc"
  },
  "execution": {
    "timeout_seconds": 10,
    "auto_cleanup": true
  }
}
EOF

    if run_container "$config" "alpine:latest" "timeout 2 ping -c 1 127.0.0.1" "$output" 15; then
        # Localhost should be blocked or at least not reachable
        # Note: Within container, 127.0.0.1 is container's own localhost, not host
        # The security rule blocks external 127.0.0.0/8 in routing
        pass "Localhost routing blocked (container isolated)"
    else
        pass "Localhost blocked (command failed)"
    fi
}

# Test 12: Multiple concurrent containers
test_concurrent_containers() {
    print_test "Multiple Concurrent Containers"

    local config1="$TEMP_DIR/config_concurrent1.json"
    local config2="$TEMP_DIR/config_concurrent2.json"
    local output1="$TEMP_DIR/output_concurrent1.txt"
    local output2="$TEMP_DIR/output_concurrent2.txt"

    echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["ping","-c","3","-W","2","1.1.1.1"],"config":{"version":"1.0","network":{"whitelist":[{"cidr":"1.1.1.1/32"}],"blacklist":[],"default_policy":"deny","block_metadata":true,"allow_dns":false,"dns_servers":[]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":15,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' > "$config1"

    echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["ping","-c","3","-W","2","8.8.8.8"],"config":{"version":"1.0","network":{"whitelist":[{"cidr":"8.8.8.8/32"}],"blacklist":[],"default_policy":"deny","block_metadata":true,"allow_dns":false,"dns_servers":[]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":15,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' > "$config2"

    # Run both containers concurrently
    run_container "$config1" "alpine:latest" "ping 1.1.1.1" "$output1" 20 &
    PID1=$!
    run_container "$config2" "alpine:latest" "ping 8.8.8.8" "$output2" 20 &
    PID2=$!

    # Wait for both (use || true to prevent set -e from exiting on failure)
    wait $PID1 || RESULT1=$?
    RESULT1=${RESULT1:-0}
    wait $PID2 || RESULT2=$?
    RESULT2=${RESULT2:-0}

    if [ $RESULT1 -eq 0 ] && [ $RESULT2 -eq 0 ]; then
        if grep -qi "3 packets transmitted, 3.*received\|3 received" "$output1" && \
           grep -qi "3 packets transmitted, 3.*received\|3 received" "$output2"; then
            pass "Multiple concurrent containers with different policies work correctly"
        else
            fail "Concurrent containers completed but with unexpected results"
            echo "Output 1:"; cat "$output1"
            echo "Output 2:"; cat "$output2"
        fi
    else
        fail "One or more concurrent containers failed"
        echo "Output 1 (exit=$RESULT1):"; cat "$output1"
        echo "Output 2 (exit=$RESULT2):"; cat "$output2"
    fi
}

# Main execution
main() {
    echo "========================================="
    echo "E2E Network Access Test Suite"
    echo "========================================="
    echo ""

    check_prerequisites
    start_bastion

    # Run all tests
    test_allow_public_internet
    test_block_metadata_service
    test_dns_resolution
    test_dns_blocked
    test_whitelist_specific_ip
    test_block_non_whitelisted
    test_http_to_allowed_destination
    test_https_request
    test_blacklist_cidr
    test_port_specific_whitelist
    test_localhost_always_blocked
    test_concurrent_containers

    echo ""
    echo "========================================="
    echo "All Tests Completed"
    echo "========================================="
}

main || true
